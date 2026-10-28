// Package egresswatch agrega el tráfico del NVR registrado por ulogd2 y dnsmasq.
package egresswatch

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Flow representa un paquete registrado por ulogd2.
type Flow struct {
	Action, Direction, Src, Dst, Proto string
	Sport, Dport, Length               int
}

// DNS representa una consulta o respuesta registrada por dnsmasq.
type DNS struct {
	Kind, Name, Value, Client, QType string
}

var (
	protocols = map[int]string{1: "icmp", 6: "tcp", 17: "udp", 58: "icmpv6", 132: "sctp"}
	fieldKeys = map[string][]string{
		"src":    {"src_ip", "ip.saddr.str", "ip6.saddr.str", "ip.saddr"},
		"dst":    {"dest_ip", "ip.daddr.str", "ip6.daddr.str", "ip.daddr"},
		"sport":  {"src_port", "tcp.sport", "udp.sport", "sctp.sport"},
		"dport":  {"dest_port", "tcp.dport", "udp.dport", "sctp.dport"},
		"proto":  {"ip.protocol", "ip6.nexthdr", "protocol"},
		"length": {"ip.totlen", "ip6.payloadlen", "raw.pktlen", "oob.len"},
		"prefix": {"oob.prefix", "prefix"},
	}
	queryPattern  = regexp.MustCompile(`query\[([A-Za-z0-9]+)\] (\S+) from (\S+)`)
	answerPattern = regexp.MustCompile(`\b(reply|cached|config|forwarded)\s+(\S+)\s+(?:is|to)\s+(\S+)`)
)

func getField(obj map[string]any, field string) any {
	for _, key := range fieldKeys[field] {
		if value, ok := obj[key]; ok && value != nil && fmt.Sprint(value) != "" {
			return value
		}
	}
	return nil
}

func intValue(value any) int {
	switch value := value.(type) {
	case float64:
		return int(value)
	case json.Number:
		n, _ := strconv.Atoi(value.String())
		return n
	case string:
		n, _ := strconv.Atoi(value)
		return n
	case int:
		return value
	default:
		return 0
	}
}

// ParsePrefix separa acción y sentido de un prefijo opennvr de nftables.
func ParsePrefix(prefix string) (action, direction string, ok bool) {
	parts := strings.Split(strings.TrimSpace(prefix), "-")
	if len(parts) < 3 || parts[0] != "opennvr" {
		return "", "", false
	}
	return parts[1], parts[2], true
}

// ParseUlogLine interpreta las variantes conocidas del JSON de ulogd2.
func ParseUlogLine(line string) (Flow, bool) {
	line = strings.TrimSpace(line)
	if line == "" {
		return Flow{}, false
	}
	var obj map[string]any
	if err := json.Unmarshal([]byte(line), &obj); err != nil || obj == nil {
		return Flow{}, false
	}
	action, direction, ok := ParsePrefix(fmt.Sprint(getField(obj, "prefix")))
	if !ok {
		return Flow{}, false
	}
	protoValue := getField(obj, "proto")
	proto := "?"
	if protoValue != nil {
		if known, exists := protocols[intValue(protoValue)]; exists {
			proto = known
		} else {
			proto = fmt.Sprint(protoValue)
		}
	}
	text := func(field string) string {
		value := getField(obj, field)
		if value == nil {
			return "?"
		}
		return fmt.Sprint(value)
	}
	return Flow{Action: action, Direction: direction, Src: text("src"), Dst: text("dst"),
		Proto: proto, Sport: intValue(getField(obj, "sport")), Dport: intValue(getField(obj, "dport")),
		Length: intValue(getField(obj, "length"))}, true
}

// ParseDNSMasqLine interpreta una línea de log-queries=extra de dnsmasq.
func ParseDNSMasqLine(line string) (DNS, bool) {
	if match := queryPattern.FindStringSubmatch(line); match != nil {
		return DNS{Kind: "query", Name: strings.TrimSuffix(strings.ToLower(match[2]), "."), Client: match[3], QType: match[1]}, true
	}
	if match := answerPattern.FindStringSubmatch(line); match != nil {
		return DNS{Kind: match[1], Name: strings.TrimSuffix(strings.ToLower(match[2]), "."), Value: match[3]}, true
	}
	return DNS{}, false
}
