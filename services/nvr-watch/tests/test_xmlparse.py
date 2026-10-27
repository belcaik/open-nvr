import unittest

from nvrwatch import xmlparse

NS = 'xmlns="http://www.zwcloud.wang/ver10/XMLSchema"'

DEVICE_INFO = f"""<?xml version="1.0" encoding="UTF-8"?>
<DeviceInfo {NS} version="1.0">
  <DeviceName>NVR</DeviceName>
  <SerialNum>ABC123456</SerialNum>
  <SoftWareVersion>V8.2.3.1</SoftWareVersion>
  <VideoInputChannels>10</VideoInputChannels>
</DeviceInfo>"""

FRONT_LIST = f"""<DevConnectList {NS}>
  <DevConnect><ChannelID>1</ChannelID><IP>192.168.0.14</IP><MAC>aa:bb:cc:dd:ee:ff</MAC>
    <Username>admin</Username><Password>camsecret</Password><ProtocolType>3</ProtocolType></DevConnect>
  <DevConnect><ChannelID>2</ChannelID><IP></IP><MAC></MAC><Username>admin</Username>
    <Password></Password></DevConnect>
</DevConnectList>"""

P2P = f"""<P2PList {NS}><P2P><Enable>true</Enable><UUID>TUTK-UID-0001</UUID>
  <State>internet-ready</State></P2P></P2PList>"""

INTERFACES = f"""<InterfaceList {NS}><Interface><ID>1</ID><AddressingType>static</AddressingType>
  <IPAddress>192.168.0.227</IPAddress><Gateway><IPAddress>192.168.0.1</IPAddress></Gateway>
  <PrimaryDNS>1.1.1.1</PrimaryDNS><SecondaryDNS>8.8.8.8</SecondaryDNS>
  <MacAddress>5a:5a:02:5b:e4:18</MacAddress></Interface></InterfaceList>"""


class XmlParseTest(unittest.TestCase):
    def test_device_info_strips_namespace_and_hashes_serial(self):
        v = xmlparse.device_info(DEVICE_INFO.encode())
        self.assertEqual(v["device.SoftWareVersion"], "V8.2.3.1")
        self.assertTrue(v["device.SerialNum"].startswith("sha256:"))
        self.assertNotIn("ABC123456", repr(v))

    def test_cameras_keyed_by_channel_without_credentials(self):
        v = xmlparse.cameras(FRONT_LIST.encode())
        self.assertEqual(v["camera.1.IP"], "192.168.0.14")
        self.assertEqual(v["camera.1.MAC"], "aa:bb:cc:dd:ee:ff")
        self.assertFalse(any(k.startswith("camera.2.") for k in v), "canal vacío omitido")
        text = repr(v)
        self.assertNotIn("camsecret", text)
        self.assertNotIn("Password", text)
        self.assertNotIn("Username", text)

    def test_p2p_uuid_hashed(self):
        v = xmlparse.p2p(P2P)
        self.assertEqual(v["p2p.1.Enable"], "true")
        self.assertEqual(v["p2p.1.State"], "internet-ready")
        self.assertNotIn("TUTK-UID-0001", repr(v))
        # Mismo valor -> mismo hash: un cambio de UUID se detecta.
        self.assertEqual(xmlparse.p2p(P2P)["p2p.1.UUID"], v["p2p.1.UUID"])

    def test_network_gateway_and_dns(self):
        v = xmlparse.network(INTERFACES)
        self.assertEqual(xmlparse.first_value(v, ".Gateway.IPAddress"), "192.168.0.1")
        self.assertEqual(v["net.1.PrimaryDNS"], "1.1.1.1")
        self.assertEqual(v["net.1.MacAddress"], "5a:5a:02:5b:e4:18")

    def test_repeated_siblings_indexed(self):
        v = xmlparse.flatten(xmlparse.parse("<a><b>1</b><b>2</b><c>3</c></a>"), "x")
        self.assertEqual(v, {"x.b[0]": "1", "x.b[1]": "2", "x.c": "3"})

    def test_invalid_xml_raises_value_error(self):
        with self.assertRaises(ValueError):
            xmlparse.device_info(b"<html>401 Unauthorized")


if __name__ == "__main__":
    unittest.main()
