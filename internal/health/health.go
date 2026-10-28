// Package health implementa el latido de los vigilantes y las
// comprobaciones que usan los healthchecks de los contenedores.
package health

import (
	"fmt"
	"net/http"
	"os"
	"time"
)

// Beat marca el archivo de latido con la hora actual.
func Beat(path string) error {
	return os.WriteFile(path, []byte(time.Now().UTC().Format(time.RFC3339)+"\n"), 0o644)
}

// Check falla si el latido no existe o es más antiguo que maxAge.
func Check(path string, maxAge time.Duration, now time.Time) error {
	st, err := os.Stat(path)
	if err != nil {
		return err
	}
	if age := now.Sub(st.ModTime()); age > maxAge {
		return fmt.Errorf("latido de hace %s (máximo %s)", age.Round(time.Second), maxAge)
	}
	return nil
}

// CheckURL exige un 200 en menos de 3 s.
func CheckURL(url string) error {
	c := &http.Client{Timeout: 3 * time.Second}
	resp, err := c.Get(url)
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return nil
}
