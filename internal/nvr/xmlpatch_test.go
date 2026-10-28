package nvr

import (
	"bytes"
	"testing"
)

func TestPatchXMLChangesNestedLeafAndPreservesDocument(t *testing.T) {
	doc := []byte(`<?xml version="1.0"?><Root keep="yes">
  <Schedule><TimeBlockList><TimeBlock_0>0000</TimeBlock_0></TimeBlockList></Schedule>
  <Untouched attr="1"> igual </Untouched>
</Root>`)
	want := bytes.Replace(doc, []byte(">0000<"), []byte(">1010<"), 1)

	got, err := PatchXML(doc, map[string]string{
		"Schedule/TimeBlockList/TimeBlock_0": "1010",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("PatchXML = %q, quiero %q", got, want)
	}
}

func TestPatchXMLEscapesLeafText(t *testing.T) {
	doc := []byte(`<Root><Value>anterior</Value></Root>`)
	got, err := PatchXML(doc, map[string]string{"Value": `<nuevo & "seguro">`})
	if err != nil {
		t.Fatal(err)
	}
	want := []byte(`<Root><Value>&lt;nuevo &amp; &#34;seguro&#34;&gt;</Value></Root>`)
	if !bytes.Equal(got, want) {
		t.Fatalf("PatchXML = %q, quiero %q", got, want)
	}
}

func TestPatchXMLRejectsMissingAndAmbiguousPathsWithoutChangingInput(t *testing.T) {
	tests := []struct {
		name    string
		doc     []byte
		changes map[string]string
	}{
		{
			name:    "ruta inexistente",
			doc:     []byte(`<Root><Value>original</Value></Root>`),
			changes: map[string]string{"Missing": "nuevo"},
		},
		{
			name:    "hermanos repetidos",
			doc:     []byte(`<Root><Value>uno</Value><Value>dos</Value></Root>`),
			changes: map[string]string{"Value": "nuevo"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			before := append([]byte(nil), tt.doc...)
			if _, err := PatchXML(tt.doc, tt.changes); err == nil {
				t.Fatal("PatchXML = nil, quiero error")
			}
			if !bytes.Equal(tt.doc, before) {
				t.Fatalf("PatchXML modificó la entrada: %q", tt.doc)
			}
		})
	}
}
