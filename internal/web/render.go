package web

import (
	"bytes"
	"fmt"
	"html/template"
	"io/fs"
	"net/http"
	"strings"

	"github.com/belcaik/open-nvr/internal/logx"
	"github.com/belcaik/open-nvr/internal/nvr"
)

// AssetVersion invalida la caché de /static/ en cada versión desplegada.
var AssetVersion = "dev"

var funcs = template.FuncMap{
	// asset añade la versión a la URL de un recurso estático.
	"asset": func(path string) string { return path + "?v=" + AssetVersion },
	// list crea una lista de opciones en la plantilla.
	"list": func(xs ...string) []string { return xs },
	// Valores admitidos por el NVR (una sola fuente: el paquete nvr).
	"flipModes":         func() []string { return nvr.FlipModes },
	"wdrModes":          func() []string { return nvr.WDRModes },
	"nrModes":           func() []string { return nvr.NoiseReduceModes },
	"blcModes":          func() []string { return nvr.BLCModes },
	"irModes":           func() []string { return nvr.IrCutModes },
	"motionLevels":      func() []string { return nvr.MotionSensitivities },
	"whiteBalanceModes": func() []string { return nvr.WhiteBalanceModes },
	"irisTypes":         func() []string { return nvr.IrisTypes },
	"exposureShutters":  func() []string { return nvr.ExposureShutters },
	"exposureGains":     func() []string { return nvr.ExposureGains },
	"audioInputMethods": func() []string { return nvr.AudioInputMethods },
	"days":              func() []int { return []int{0, 1, 2, 3, 4, 5, 6} },
	"slots": func() []int {
		xs := make([]int, 48)
		for i := range xs {
			xs[i] = i
		}
		return xs
	},
	"slotName": func(day, slot int) string { return "slot_" + itoa(day) + "_" + itoa(slot) },
	"slotOn":   func(days [7][48]bool, day, slot int) bool { return days[day][slot] },
	"slotTime": func(slot int) string { return fmt.Sprintf("%02d:%02d", slot/2, (slot%2)*30) },
	"dayName":  func(day int) string { return []string{"Dom", "Lun", "Mar", "Mié", "Jue", "Vie", "Sáb"}[day] },
	"logTime":  formatLogTime,
	"choiceLabel": func(value string) string {
		labels := map[string]string{
			"incandescent": "Incandescente", "warm": "Cálido", "fluorescent": "Fluorescente",
			"natural": "Natural", "manual": "Manual", "auto": "Automático", "locked": "Bloqueado",
			"DC-Iris": "Iris automático", "microphone": "Micrófono", "line-in": "Entrada de línea",
			"0": "Desactivado", "30": "Débil", "60": "Medio", "90": "Fuerte",
			"internet-ready": "conectado a Internet", "offline": "sin conexión",
		}
		if label := labels[value]; label != "" {
			return label
		}
		return value
	},
}

// parsePages compila cada página junto con layout.html. Los parciales
// (nombre que empieza por "_") se compilan solos.
func parsePages() map[string]*template.Template {
	pages := map[string]*template.Template{}
	names, _ := fs.Glob(templateFS, "templates/*.html")
	for _, n := range names {
		base := strings.TrimSuffix(strings.TrimPrefix(n, "templates/"), ".html")
		if base == "layout" {
			continue
		}
		if strings.HasPrefix(base, "_") {
			pages[base] = template.Must(template.New(base+".html").Funcs(funcs).ParseFS(templateFS, n))
			continue
		}
		pages[base] = template.Must(template.New("layout.html").Funcs(funcs).ParseFS(templateFS, "templates/layout.html", n))
	}
	return pages
}

// pageData es lo que reciben todas las plantillas.
type pageData struct {
	Title string
	User  string
	CSRF  string
	Data  any
}

func (s *server) render(w http.ResponseWriter, r *http.Request, status int, page, title string, data any) {
	pd := pageData{Title: title, Data: data}
	if se := sessionFrom(r); se.ID != "" {
		pd.User, pd.CSRF = se.User, s.d.Sessions.CSRFToken(se)
	}
	t := s.pages[page]
	name := "layout"
	if strings.HasPrefix(page, "_") {
		name = page
	}
	var buf bytes.Buffer
	if err := t.ExecuteTemplate(&buf, name+".html", pd); err != nil {
		logx.Printf("plantilla %s: %v", page, err)
		http.Error(w, "Error interno", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	buf.WriteTo(w)
}
