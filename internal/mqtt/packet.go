package mqtt

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
	"unicode/utf8"
)

const maxPacketSize = 64 * 1024

var errPacketTooLarge = errors.New("mqtt: paquete entrante supera 64 KiB")

type packet struct {
	header byte
	body   []byte
}

func readPacket(r *bufio.Reader) (packet, error) {
	header, err := r.ReadByte()
	if err != nil {
		return packet{}, err
	}
	remaining, width, err := readRemainingLength(r)
	if err != nil {
		return packet{}, err
	}
	if 1+width+remaining > maxPacketSize {
		return packet{}, errPacketTooLarge
	}
	body := make([]byte, remaining)
	if _, err := io.ReadFull(r, body); err != nil {
		return packet{}, fmt.Errorf("mqtt: leer paquete: %w", err)
	}
	return packet{header: header, body: body}, nil
}

func readRemainingLength(r *bufio.Reader) (int, int, error) {
	value, multiplier := 0, 1
	for width := 1; width <= 4; width++ {
		b, err := r.ReadByte()
		if err != nil {
			return 0, width, fmt.Errorf("mqtt: leer remaining length: %w", err)
		}
		value += int(b&0x7f) * multiplier
		if b&0x80 == 0 {
			if width > 1 && value < multiplier {
				return 0, width, errors.New("mqtt: remaining length mal formado")
			}
			return value, width, nil
		}
		multiplier *= 128
	}
	return 0, 4, errors.New("mqtt: remaining length usa más de 4 bytes")
}

func appendRemainingLength(dst []byte, n int) []byte {
	for {
		encoded := byte(n % 128)
		n /= 128
		if n > 0 {
			encoded |= 0x80
		}
		dst = append(dst, encoded)
		if n == 0 {
			return dst
		}
	}
}

func encodePacket(header byte, body []byte) []byte {
	result := make([]byte, 0, 1+4+len(body))
	result = append(result, header)
	result = appendRemainingLength(result, len(body))
	return append(result, body...)
}

func appendString(dst []byte, value string) []byte {
	dst = binary.BigEndian.AppendUint16(dst, uint16(len(value)))
	return append(dst, value...)
}

func encodePublish(message Message) ([]byte, error) {
	if err := validateTopic(message.Topic); err != nil {
		return nil, err
	}
	body := appendString(nil, message.Topic)
	body = append(body, message.Payload...)
	header := byte(0x30)
	if message.Retain {
		header |= 0x01
	}
	return encodePacket(header, body), nil
}

func encodeConnect(clientID, user, pass string, will *Message, keepAlive time.Duration) ([]byte, error) {
	if len(clientID) > 65535 || !utf8.ValidString(clientID) || strings.ContainsRune(clientID, '\x00') {
		return nil, errors.New("mqtt: client ID inválido")
	}
	flags := byte(0x02)
	if user != "" || pass != "" {
		flags |= 0x80
	}
	if pass != "" {
		flags |= 0x40
	}
	if will != nil {
		if err := validateTopic(will.Topic); err != nil {
			return nil, fmt.Errorf("mqtt: will: %w", err)
		}
		if len(will.Payload) > 65535 {
			return nil, errors.New("mqtt: payload de will supera 65535 bytes")
		}
		flags |= 0x04
		if will.Retain {
			flags |= 0x20
		}
	}
	if len(user) > 65535 || len(pass) > 65535 || !utf8.ValidString(user) || strings.ContainsRune(user, '\x00') {
		return nil, errors.New("mqtt: credenciales inválidas")
	}

	body := appendString(nil, "MQTT")
	body = append(body, 0x04, flags)
	seconds := keepAlive / time.Second
	if seconds > 65535 {
		seconds = 65535
	}
	body = binary.BigEndian.AppendUint16(body, uint16(seconds))
	body = appendString(body, clientID)
	if will != nil {
		body = appendString(body, will.Topic)
		body = binary.BigEndian.AppendUint16(body, uint16(len(will.Payload)))
		body = append(body, will.Payload...)
	}
	if user != "" || pass != "" {
		body = appendString(body, user)
	}
	if pass != "" {
		body = appendString(body, pass)
	}
	return encodePacket(0x10, body), nil
}

func encodeSubscribe(id uint16, filter string) ([]byte, error) {
	if id == 0 {
		return nil, errors.New("mqtt: packet ID de SUBSCRIBE no puede ser cero")
	}
	if err := validateFilter(filter); err != nil {
		return nil, err
	}
	body := binary.BigEndian.AppendUint16(nil, id)
	body = appendString(body, filter)
	body = append(body, 0x00)
	return encodePacket(0x82, body), nil
}

func parsePublish(in packet) (Message, error) {
	if in.header>>4 != 3 || in.header&0x06 != 0 {
		return Message{}, errors.New("mqtt: PUBLISH entrante con QoS distinto de 0")
	}
	topic, payload, err := parseString(in.body)
	if err != nil {
		return Message{}, fmt.Errorf("mqtt: PUBLISH inválido: %w", err)
	}
	if err := validateTopic(topic); err != nil {
		return Message{}, fmt.Errorf("mqtt: PUBLISH inválido: %w", err)
	}
	return Message{Topic: topic, Payload: payload, Retain: in.header&0x01 != 0}, nil
}

func validateTopic(topic string) error {
	if err := validateUTF8Name(topic); err != nil {
		return err
	}
	if strings.ContainsAny(topic, "+#") {
		return errors.New("mqtt: el topic de publicación contiene comodines")
	}
	return nil
}

func validateFilter(filter string) error {
	if err := validateUTF8Name(filter); err != nil {
		return err
	}
	levels := strings.Split(filter, "/")
	for i, level := range levels {
		if strings.Contains(level, "#") && (level != "#" || i != len(levels)-1) {
			return errors.New("mqtt: comodín # mal formado")
		}
		if strings.Contains(level, "+") && level != "+" {
			return errors.New("mqtt: comodín + mal formado")
		}
	}
	return nil
}

func validateUTF8Name(value string) error {
	if len(value) == 0 || len(value) > 65535 {
		return errors.New("mqtt: longitud fuera del intervalo 1–65535")
	}
	if !utf8.ValidString(value) {
		return errors.New("mqtt: texto no es UTF-8 válido")
	}
	if strings.ContainsRune(value, '\x00') {
		return errors.New("mqtt: texto contiene U+0000")
	}
	return nil
}

func matchFilter(filter, topic string) bool {
	filterLevels := strings.Split(filter, "/")
	topicLevels := strings.Split(topic, "/")
	for i, level := range filterLevels {
		if level == "#" {
			return i <= len(topicLevels)
		}
		if i >= len(topicLevels) || level != "+" && level != topicLevels[i] {
			return false
		}
	}
	return len(filterLevels) == len(topicLevels)
}

func parseString(body []byte) (string, []byte, error) {
	if len(body) < 2 {
		return "", nil, errors.New("mqtt: cadena truncada")
	}
	n := int(binary.BigEndian.Uint16(body))
	if len(body) < 2+n {
		return "", nil, errors.New("mqtt: cadena truncada")
	}
	value := string(body[2 : 2+n])
	if err := validateUTF8Name(value); err != nil {
		return "", nil, err
	}
	return value, body[2+n:], nil
}
