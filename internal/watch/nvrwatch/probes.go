package nvrwatch

import (
	"bufio"
	"context"
	"crypto/md5"
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const maxBody = 1 << 20

type Probes interface {
	TCP(host string, port int) (bool, string)
	HTTPGet(host string, port int, path, user, password string) (int, map[string]string, []byte, error)
	RTSPDescribe(host string, port int, path, user, password string) (bool, string)
	Scan(host string, ports []int, sleep func(time.Duration)) []int
}

type networkProbes struct{}

func (networkProbes) TCP(host string, port int) (bool, string) {
	conn, err := net.DialTimeout("tcp", net.JoinHostPort(host, strconv.Itoa(port)), 3*time.Second)
	if err == nil {
		conn.Close()
		return true, "abierto"
	}
	if timeout, ok := err.(net.Error); ok && timeout.Timeout() {
		return false, "timeout"
	}
	if strings.Contains(strings.ToLower(err.Error()), "refused") {
		return false, "rechazado"
	}
	return false, fmt.Sprintf("%T", err)
}

func (networkProbes) HTTPGet(host string, port int, path, user, password string) (int, map[string]string, []byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+net.JoinHostPort(host, strconv.Itoa(port))+path, nil)
	if err != nil {
		return 0, nil, nil, err
	}
	req.Header.Set("User-Agent", "open-nvr-watch/1")
	req.Header.Set("Connection", "close")
	if user != "" || password != "" {
		req.SetBasicAuth(user, password)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, nil, nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return 0, nil, nil, err
	}
	headers := map[string]string{}
	for key, values := range resp.Header {
		if len(values) > 0 {
			headers[strings.ToLower(key)] = values[0]
		}
	}
	return resp.StatusCode, headers, body, nil
}

func (p networkProbes) Scan(host string, ports []int, sleep func(time.Duration)) []int {
	var open []int
	for _, port := range ports {
		conn, err := net.DialTimeout("tcp", net.JoinHostPort(host, strconv.Itoa(port)), time.Second)
		if err == nil {
			open = append(open, port)
			conn.Close()
		}
		sleep(200 * time.Millisecond)
	}
	return open
}

func basicAuth(user, password string) string {
	return "Basic " + base64.StdEncoding.EncodeToString([]byte(user+":"+password))
}

var (
	digestPattern        = regexp.MustCompile(`(?i)WWW-Authenticate:\s*Digest\s+(.*)`)
	paramPattern         = regexp.MustCompile(`(\w+)="([^"]*)"`)
	contentLengthPattern = regexp.MustCompile(`(?im)^content-length:\s*(\d+)`)
)

func rtspAuth(header, user, password, url string) string {
	match := digestPattern.FindStringSubmatch(header)
	if match == nil {
		return basicAuth(user, password)
	}
	params := map[string]string{}
	for _, pair := range paramPattern.FindAllStringSubmatch(match[1], -1) {
		params[pair[1]] = pair[2]
	}
	ha1 := fmt.Sprintf("%x", md5.Sum([]byte(user+":"+params["realm"]+":"+password)))
	ha2 := fmt.Sprintf("%x", md5.Sum([]byte("DESCRIBE:"+url)))
	response := fmt.Sprintf("%x", md5.Sum([]byte(ha1+":"+params["nonce"]+":"+ha2)))
	return fmt.Sprintf(`Digest username="%s", realm="%s", nonce="%s", uri="%s", response="%s"`, user, params["realm"], params["nonce"], url, response)
}

func rtspRequest(conn net.Conn, reader *bufio.Reader, url string, cseq int, authorization string) (string, error) {
	request := fmt.Sprintf("DESCRIBE %s RTSP/1.0\r\nCSeq: %d\r\nAccept: application/sdp\r\nUser-Agent: open-nvr-watch/1\r\n", url, cseq)
	if authorization != "" {
		request += "Authorization: " + authorization + "\r\n"
	}
	if _, err := io.WriteString(conn, request+"\r\n"); err != nil {
		return "", err
	}
	var header strings.Builder
	for header.Len() < 65536 {
		line, err := reader.ReadString('\n')
		header.WriteString(line)
		text := header.String()
		if strings.HasSuffix(text, "\r\n\r\n") || strings.HasSuffix(text, "\n\n") {
			break
		}
		if err != nil {
			return text, err
		}
	}
	text := header.String()
	if match := contentLengthPattern.FindStringSubmatch(text); match != nil {
		length, _ := strconv.Atoi(match[1])
		if length > 65536 {
			length = 65536
		}
		_, _ = io.CopyN(io.Discard, reader, int64(length))
	}
	return text, nil
}

func rtspStatus(header string) int {
	fields := strings.Fields(header)
	if len(fields) < 2 || !strings.HasPrefix(fields[0], "RTSP/") {
		return 0
	}
	status, _ := strconv.Atoi(fields[1])
	return status
}

func (networkProbes) RTSPDescribe(host string, port int, path, user, password string) (bool, string) {
	address := net.JoinHostPort(host, strconv.Itoa(port))
	conn, err := net.DialTimeout("tcp", address, 5*time.Second)
	if err != nil {
		if timeout, ok := err.(net.Error); ok && timeout.Timeout() {
			return false, "timeout"
		}
		return false, fmt.Sprintf("%T", err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(5 * time.Second))
	url := "rtsp://" + address + path
	reader := bufio.NewReader(conn)
	header, err := rtspRequest(conn, reader, url, 1, "")
	if err != nil {
		return false, fmt.Sprintf("%T", err)
	}
	status := rtspStatus(header)
	if status == http.StatusUnauthorized && (user != "" || password != "") {
		header, err = rtspRequest(conn, reader, url, 2, rtspAuth(header, user, password, url))
		if err != nil {
			return false, fmt.Sprintf("%T", err)
		}
		status = rtspStatus(header)
	}
	if status == 0 {
		return false, "respuesta no RTSP"
	}
	if user != "" || password != "" {
		return status == http.StatusOK, fmt.Sprintf("RTSP %d", status)
	}
	return true, fmt.Sprintf("RTSP %d", status)
}

func httpDateDrift(headers map[string]string, now float64) *float64 {
	value := headers["date"]
	if value == "" {
		return nil
	}
	parsed, err := http.ParseTime(value)
	if err != nil {
		return nil
	}
	drift := float64(parsed.UnixNano())/float64(time.Second) - now
	return &drift
}
