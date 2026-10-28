// Package nvrwatch vigila la disponibilidad y la línea base del NVR.
package nvrwatch

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"fmt"
	"io"
	"slices"
	"strings"
)

type xmlNode struct {
	XMLName  xml.Name
	Text     string    `xml:",chardata"`
	Children []xmlNode `xml:",any"`
}

func parseXML(data []byte) (xmlNode, error) {
	var root xmlNode
	dec := xml.NewDecoder(strings.NewReader(string(data)))
	if err := dec.Decode(&root); err != nil {
		if err == io.EOF {
			err = fmt.Errorf("EOF")
		}
		return xmlNode{}, fmt.Errorf("XML no válido: %v", err)
	}
	return root, nil
}

func shortHash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return "sha256:" + hex.EncodeToString(sum[:])[:12]
}

func fieldName(path string) string {
	parts := strings.Split(path, ".")
	last := parts[len(parts)-1]
	if i := strings.IndexByte(last, '['); i >= 0 {
		last = last[:i]
	}
	return strings.ToLower(last)
}

func flatten(root xmlNode, prefix string) map[string]string {
	out := map[string]string{}
	var walk func(xmlNode, string)
	walk = func(node xmlNode, path string) {
		if len(node.Children) == 0 {
			if path == "" {
				return
			}
			field := fieldName(path)
			if field == "username" || field == "password" || field == "senderemailpassword" || field == "userpassword" {
				return
			}
			value := strings.TrimSpace(node.Text)
			switch field {
			case "uuid", "serialnum", "serialnumber", "did", "p2pid":
				if value != "" {
					value = shortHash(value)
				}
			}
			out[path] = value
			return
		}
		counts := map[string]int{}
		for _, child := range node.Children {
			counts[child.XMLName.Local]++
		}
		seen := map[string]int{}
		for _, child := range node.Children {
			name := child.XMLName.Local
			if counts[name] > 1 {
				name = fmt.Sprintf("%s[%d]", name, seen[name])
				seen[child.XMLName.Local]++
			}
			if path != "" {
				name = path + "." + name
			}
			walk(child, name)
		}
	}
	walk(root, prefix)
	return out
}

func deviceInfo(data []byte) (map[string]string, error) {
	root, err := parseXML(data)
	if err != nil {
		return nil, err
	}
	return flatten(root, "device"), nil
}

func records(root xmlNode, tag string) []xmlNode {
	var out []xmlNode
	var walk func(xmlNode)
	walk = func(node xmlNode) {
		if node.XMLName.Local == tag {
			out = append(out, node)
		}
		for _, child := range node.Children {
			walk(child)
		}
	}
	walk(root)
	return out
}

func recordKey(node xmlNode, index int, keyFields ...string) string {
	for _, field := range keyFields {
		for _, child := range node.Children {
			if child.XMLName.Local == field && strings.TrimSpace(child.Text) != "" {
				return strings.TrimSpace(child.Text)
			}
		}
	}
	return fmt.Sprintf("%d", index+1)
}

func keyedRecords(root xmlNode, tag, section string, keyFields ...string) map[string]string {
	out := map[string]string{}
	for i, rec := range records(root, tag) {
		key := recordKey(rec, i, keyFields...)
		for name, value := range flatten(rec, section+"."+key) {
			out[name] = value
		}
	}
	return out
}

func cameras(data []byte) (map[string]string, error) {
	root, err := parseXML(data)
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	for i, rec := range records(root, "DevConnect") {
		flat := flatten(rec, "x")
		ip, mac := flat["x.IP"], flat["x.MAC"]
		if (ip == "" || ip == "0.0.0.0") && (mac == "" || mac == "00:00:00:00:00:00") {
			continue
		}
		key := recordKey(rec, i, "ChannelID")
		for name, value := range flat {
			out["camera."+key+"."+strings.TrimPrefix(name, "x.")] = value
		}
	}
	return out, nil
}

func p2p(data []byte) (map[string]string, error) {
	root, err := parseXML(data)
	if err != nil {
		return nil, err
	}
	return keyedRecords(root, "P2P", "p2p"), nil
}

func networkValues(data []byte) (map[string]string, error) {
	root, err := parseXML(data)
	if err != nil {
		return nil, err
	}
	return keyedRecords(root, "Interface", "net", "ID"), nil
}

func cameraStatus(data []byte) (map[string]string, error) {
	root, err := parseXML(data)
	if err != nil {
		return nil, err
	}
	return keyedRecords(root, "DevStatus", "camstatus", "ChannelID", "ID"), nil
}

func ntpValues(data []byte) (map[string]string, error) {
	root, err := parseXML(data)
	if err != nil {
		return nil, err
	}
	return flatten(root, "ntp"), nil
}

func deviceStatus(data []byte) (map[string]string, error) {
	root, err := parseXML(data)
	if err != nil {
		return nil, err
	}
	return flatten(root, "status"), nil
}

func firstValue(values map[string]string, suffix string) string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	for _, key := range keys {
		if strings.HasSuffix(key, suffix) {
			return values[key]
		}
	}
	return ""
}
