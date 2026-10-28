package nvr

import (
	"context"
	"fmt"
	"net/http"
)

// Snapshot descarga una captura JPEG del canal:
// GET /Snapshot/{ch}/RemoteImageCapture?ImageFormat=2. Usa un timeout más
// largo que el resto porque el NVR tarda en generar la imagen.
func (c *Client) Snapshot(ctx context.Context, ch int) ([]byte, error) {
	return c.do(ctx, http.MethodGet,
		fmt.Sprintf("/Snapshot/%d/RemoteImageCapture?ImageFormat=2", ch), "", snapshotTimeout)
}
