package egresswatch

import "testing"

func TestParseUlogLine(t *testing.T) {
	tests := []struct {
		name string
		line string
		want Flow
		ok   bool
	}{
		{"claves cortas", `{"timestamp":"2026-10-26T10:00:00","oob.prefix":"opennvr-drop-out ","src_ip":"192.168.0.227","dest_ip":"203.0.113.5","src_port":40000,"dest_port":8090,"ip.protocol":6,"ip.totlen":60}`, Flow{Action: "drop", Direction: "out", Src: "192.168.0.227", Dst: "203.0.113.5", Proto: "tcp", Sport: 40000, Dport: 8090, Length: 60}, true},
		{"claves largas", `{"oob.prefix":"opennvr-fwd-in","ip.saddr.str":"203.0.113.5","ip.daddr.str":"192.168.0.227","udp.sport":123,"udp.dport":40001,"ip.protocol":17,"ip.totlen":"76"}`, Flow{Action: "fwd", Direction: "in", Src: "203.0.113.5", Dst: "192.168.0.227", Proto: "udp", Sport: 123, Dport: 40001, Length: 76}, true},
		{"icmp sin puertos", `{"oob.prefix":"opennvr-drop-out","src_ip":"192.168.0.227","dest_ip":"1.1.1.1","ip.protocol":1,"ip.totlen":84}`, Flow{Action: "drop", Direction: "out", Src: "192.168.0.227", Dst: "1.1.1.1", Proto: "icmp", Length: 84}, true},
		{"prefijo ajeno", `{"oob.prefix":"other-thing"}`, Flow{}, false},
		{"json roto", `{not json`, Flow{}, false},
		{"vacía", ``, Flow{}, false},
		{"lista", `[1,2]`, Flow{}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := ParseUlogLine(tt.line)
			if ok != tt.ok || got != tt.want {
				t.Fatalf("ParseUlogLine() = %+v, %v; want %+v, %v", got, ok, tt.want, tt.ok)
			}
		})
	}
}

func TestParsePrefix(t *testing.T) {
	action, direction, ok := ParsePrefix("opennvr-drop-v6 ")
	if !ok || action != "drop" || direction != "v6" {
		t.Fatalf("ParsePrefix() = %q, %q, %v", action, direction, ok)
	}
	if _, _, ok := ParsePrefix("opennvr-x"); ok {
		t.Fatal("un prefijo incompleto no debe aceptarse")
	}
}

func TestParseDnsmasqLine(t *testing.T) {
	query, ok := ParseDNSMasqLine("Oct 26 10:00:00 dnsmasq[1]: 12 192.168.0.227/41234 query[A] erp.zwcloud.wang from 192.168.0.227")
	if !ok || query != (DNS{Kind: "query", Name: "erp.zwcloud.wang", Client: "192.168.0.227", QType: "A"}) {
		t.Fatalf("query = %+v, %v", query, ok)
	}
	reply, ok := ParseDNSMasqLine("Oct 26 10:00:00 dnsmasq[1]: 12 192.168.0.227/41234 reply pool.ntp.org is 203.0.113.7")
	if !ok || reply.Kind != "reply" || reply.Name != "pool.ntp.org" || reply.Value != "203.0.113.7" {
		t.Fatalf("reply = %+v, %v", reply, ok)
	}
	nx, ok := ParseDNSMasqLine("Oct 26 10:00:00 dnsmasq[1]: 13 192.168.0.227/41235 config utilerp.p6sai.com is NXDOMAIN")
	if !ok || nx.Kind != "config" || nx.Value != "NXDOMAIN" {
		t.Fatalf("nxdomain = %+v, %v", nx, ok)
	}
	if _, ok := ParseDNSMasqLine("dnsmasq[1]: started, version 2.90 cachesize 0"); ok {
		t.Fatal("una línea ajena no debe aceptarse")
	}
}
