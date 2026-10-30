package p6sclient

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"strconv"

	"github.com/belcaik/open-nvr/internal/p6s"
)

type Config struct {
	Host        string
	CommandPort int
	Username    string
	Password    string
}

type Connector struct {
	Config Config
}

func (c Connector) OpenAuthenticated(ctx context.Context) (net.Conn, net.Conn, error) {
	return Connect(ctx, c.Config)
}

func Connect(ctx context.Context, cfg Config) (net.Conn, net.Conn, error) {
	if ctx == nil {
		return nil, nil, errors.New("p6sclient: contexto nil")
	}
	if cfg.Host == "" || cfg.Username == "" || cfg.Password == "" {
		return nil, nil, errors.New("p6sclient: configuración incompleta")
	}
	if cfg.CommandPort == 0 {
		cfg.CommandPort = 6060
	}
	dialer := &net.Dialer{}
	command, err := dialer.DialContext(ctx, "tcp", net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.CommandPort)))
	if err != nil {
		return nil, nil, fmt.Errorf("p6sclient: conectar comandos: %w", err)
	}
	ok := false
	defer func() {
		if !ok {
			_ = command.Close()
		}
	}()
	ip, mac, err := localIdentity(command)
	if err != nil {
		return nil, nil, err
	}
	var random [4]byte
	if _, err := rand.Read(random[:]); err != nil {
		return nil, nil, fmt.Errorf("p6sclient: aleatorio: %w", err)
	}
	result, err := p6s.Login(ctx, command, p6s.Params{
		Username: []byte(cfg.Username), Password: []byte(cfg.Password), UserIP: ip, UserMAC: mac,
		Random: binary.BigEndian.Uint32(random[:])%32767 + 1,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("p6sclient: login: %w", err)
	}
	media, err := dialer.DialContext(ctx, "tcp", net.JoinHostPort(cfg.Host, strconv.Itoa(int(result.TCPPort))))
	if err != nil {
		return nil, nil, fmt.Errorf("p6sclient: conectar medios: %w", err)
	}
	ok = true
	return command, media, nil
}

func localIdentity(conn net.Conn) (string, string, error) {
	local, ok := conn.LocalAddr().(*net.TCPAddr)
	if !ok {
		return "", "", errors.New("p6sclient: dirección local no TCP")
	}
	interfaces, err := net.Interfaces()
	if err != nil {
		return "", "", fmt.Errorf("p6sclient: interfaces: %w", err)
	}
	for _, iface := range interfaces {
		addresses, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, address := range addresses {
			network, ok := address.(*net.IPNet)
			if ok && network.IP.Equal(local.IP) {
				return local.IP.String(), iface.HardwareAddr.String(), nil
			}
		}
	}
	return "", "", errors.New("p6sclient: no se encontró identidad local")
}
