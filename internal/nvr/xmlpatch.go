package nvr

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"sort"
	"strings"
)

// PatchXML devuelve doc cambiando únicamente el texto de las hojas indicadas.
// Las rutas parten del hijo de la raíz y se separan con "/".
func PatchXML(doc []byte, changes map[string]string) ([]byte, error) {
	type element struct {
		name         string
		contentStart int64
		hasChild     bool
	}
	type span struct {
		start int
		end   int
	}

	dec := xml.NewDecoder(bytes.NewReader(doc))
	var stack []element
	found := make(map[string][]span, len(changes))
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("nvr: XML inválido: %w", err)
		}
		switch tok := tok.(type) {
		case xml.StartElement:
			if len(stack) > 0 {
				stack[len(stack)-1].hasChild = true
			}
			stack = append(stack, element{name: tok.Name.Local, contentStart: dec.InputOffset()})
		case xml.EndElement:
			if len(stack) == 0 {
				return nil, fmt.Errorf("nvr: cierre XML inesperado %q", tok.Name.Local)
			}
			el := stack[len(stack)-1]
			if el.name != tok.Name.Local {
				return nil, fmt.Errorf("nvr: cierre XML %q no corresponde a %q", tok.Name.Local, el.name)
			}
			if !el.hasChild && len(stack) > 1 {
				parts := make([]string, 0, len(stack)-1)
				for _, parent := range stack[1:] {
					parts = append(parts, parent.name)
				}
				path := strings.Join(parts, "/")
				if _, wanted := changes[path]; wanted {
					endOffset := int(dec.InputOffset())
					rel := bytes.LastIndex(doc[el.contentStart:endOffset], []byte("</"))
					if rel < 0 {
						return nil, fmt.Errorf("nvr: no se encontró el cierre de %q", path)
					}
					found[path] = append(found[path], span{start: int(el.contentStart), end: int(el.contentStart) + rel})
				}
			}
			stack = stack[:len(stack)-1]
		}
	}

	type replacement struct {
		span
		value []byte
	}
	replacements := make([]replacement, 0, len(changes))
	for path, value := range changes {
		if path == "" || strings.HasPrefix(path, "/") || strings.HasSuffix(path, "/") || strings.Contains(path, "//") {
			return nil, fmt.Errorf("nvr: ruta XML inválida %q", path)
		}
		matches := found[path]
		if len(matches) == 0 {
			return nil, fmt.Errorf("nvr: ruta XML %q inexistente o no es una hoja", path)
		}
		if len(matches) != 1 {
			return nil, fmt.Errorf("nvr: ruta XML %q ambigua", path)
		}
		var escaped bytes.Buffer
		if err := xml.EscapeText(&escaped, []byte(value)); err != nil {
			return nil, fmt.Errorf("nvr: escapar %q: %w", path, err)
		}
		replacements = append(replacements, replacement{span: matches[0], value: escaped.Bytes()})
	}

	sort.Slice(replacements, func(i, j int) bool { return replacements[i].start > replacements[j].start })
	patched := append([]byte(nil), doc...)
	for _, repl := range replacements {
		out := make([]byte, 0, len(patched)-(repl.end-repl.start)+len(repl.value))
		out = append(out, patched[:repl.start]...)
		out = append(out, repl.value...)
		out = append(out, patched[repl.end:]...)
		patched = out
	}
	return patched, nil
}
