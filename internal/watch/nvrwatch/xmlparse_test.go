package nvrwatch

import (
	"reflect"
	"strings"
	"testing"
)

const xmlNamespace = `xmlns="http://www.zwcloud.wang"`

func TestDeviceInfoStripsNamespaceAndHashesSerial(t *testing.T) {
	xml := `<DeviceInfo ` + xmlNamespace + `><SoftWareVersion>V8.1</SoftWareVersion><SerialNum>N0000000000000000000000</SerialNum></DeviceInfo>`
	values, err := deviceInfo([]byte(xml))
	if err != nil {
		t.Fatal(err)
	}
	if values["device.SoftWareVersion"] != "V8.1" {
		t.Fatalf("SoftWareVersion = %q", values["device.SoftWareVersion"])
	}
	if !strings.HasPrefix(values["device.SerialNum"], "sha256:") || strings.Contains(values["device.SerialNum"], "N000") {
		t.Fatalf("SerialNum sensible = %q", values["device.SerialNum"])
	}
}

func TestCamerasKeyedByChannelWithoutCredentials(t *testing.T) {
	xml := `<DevConnectList ` + xmlNamespace + `><DevConnect><ChannelID>1</ChannelID><IP>192.0.2.14</IP><MAC>00:11:22:33:44:55</MAC><Username>admin</Username><Password>ficticia</Password></DevConnect><DevConnect><ChannelID>2</ChannelID><IP></IP><MAC></MAC><Username>admin</Username><Password></Password></DevConnect></DevConnectList>`
	values, err := cameras([]byte(xml))
	if err != nil {
		t.Fatal(err)
	}
	if values["camera.1.IP"] != "192.0.2.14" || values["camera.1.MAC"] != "00:11:22:33:44:55" {
		t.Fatal(values)
	}
	for key := range values {
		if strings.HasPrefix(key, "camera.2.") || strings.Contains(strings.ToLower(key), "password") || strings.Contains(strings.ToLower(key), "username") {
			t.Fatalf("clave inesperada %q", key)
		}
	}
}

func TestP2PUUIDHashed(t *testing.T) {
	xml := `<P2PList ` + xmlNamespace + `><P2P><Enable>true</Enable><UUID>XXXXXX-000000-XXXXX</UUID><State>internet-ready</State></P2P></P2PList>`
	values, err := p2p([]byte(xml))
	if err != nil {
		t.Fatal(err)
	}
	if values["p2p.1.Enable"] != "true" || values["p2p.1.State"] != "internet-ready" || strings.Contains(values["p2p.1.UUID"], "XXXXXX") {
		t.Fatal(values)
	}
	again, _ := p2p([]byte(xml))
	if again["p2p.1.UUID"] != values["p2p.1.UUID"] {
		t.Fatal("el hash no es estable")
	}
}

func TestNetworkGatewayAndDNS(t *testing.T) {
	xml := `<InterfaceList ` + xmlNamespace + `><Interface><ID>1</ID><IPAddress>192.0.2.227</IPAddress><Gateway><IPAddress>192.0.2.1</IPAddress></Gateway><PrimaryDNS>1.1.1.1</PrimaryDNS></Interface></InterfaceList>`
	values, err := networkValues([]byte(xml))
	if err != nil {
		t.Fatal(err)
	}
	if firstValue(values, ".Gateway.IPAddress") != "192.0.2.1" || values["net.1.PrimaryDNS"] != "1.1.1.1" {
		t.Fatal(values)
	}
}

func TestRepeatedSiblingsIndexed(t *testing.T) {
	root, err := parseXML([]byte(`<a><b>1</b><b>2</b><c>3</c></a>`))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"x.b[0]": "1", "x.b[1]": "2", "x.c": "3"}
	if got := flatten(root, "x"); !reflect.DeepEqual(got, want) {
		t.Fatalf("flatten = %#v", got)
	}
}

func TestInvalidXMLReturnsError(t *testing.T) {
	if _, err := deviceInfo([]byte(`<html>401 Unauthorized`)); err == nil || !strings.Contains(err.Error(), "XML no válido") {
		t.Fatalf("error = %v", err)
	}
}
