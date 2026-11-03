# opennvr-detect

Servicio Python 3.12 que lee el substream de cada cámara sin acumular frames,
detecta personas con YOLO26s-e2e mediante ONNX Runtime CPU, mantiene IDs de
seguimiento y publica el resultado por WebSocket. El modelo se monta fuera de
la imagen del contenedor.

## Configuración

- `DETECT_CAMERAS` (obligatoria): lista `id=fuente` separada por comas. Admite
  RTSP, una ruta de archivo o `file:ruta`.
- `DETECT_MODEL` (obligatoria): ruta al modelo ONNX.
- `DETECT_CONF`: confianza mínima; por defecto `0.25`.
- `DETECT_THREADS`: hilos ONNX Runtime por cámara; por defecto `2`.
- `DETECT_LISTEN`: dirección del servidor; por defecto `127.0.0.1:8556`.

Ejemplo:

```sh
DETECT_CAMERAS='1=rtsp://127.0.0.1:8554/cam1_sub' \
DETECT_MODEL=/models/yolo26s.onnx python -m opennvr_detect
```

Las URLs nunca se incluyen en los logs. RTSP utiliza TCP y reconecta con
backoff de 1 a 30 segundos. `SIGTERM` detiene lectores y servidor limpiamente.

## Contrato WebSocket

El servidor escucha en `DETECT_LISTEN`. La ruta WebSocket es `/detections` y
`GET /healthz` devuelve HTTP 200 con el último estado de cada cámara. Cada
cliente tiene una cola acotada independiente: si queda atrás pierde los
mensajes más antiguos, pero nunca frena la detección.

La web habilita las cámaras conectadas por ese mismo WebSocket:

```json
{"type":"cameras","enabled":[1]}
```

El último mensaje manda y se conserva si el cliente se desconecta. Sin ningún
mensaje se procesan todas las cámaras configuradas. Los mensajes inválidos se
registran y se ignoran sin cerrar la conexión.

Un mensaje de detecciones se publica por cada frame procesado, incluso sin
tracks:

```json
{"type":"detections","cam":1,"seq":4711,"ts":"2026-11-03T15:30:01.123456Z","w":800,"h":448,"tracks":[{"id":7,"conf":0.83,"box":[0.412,0.130,0.501,0.774]}]}
```

`box` contiene `x1,y1,x2,y2` normalizados al frame original, recortados a
`[0,1]` y con tres decimales. `seq` aumenta de uno en uno por cámara y `ts` es
la captura en UTC. Solo se publican tracks confirmados.

Cada cinco segundos y en cada cambio de estado se publica:

```json
{"type":"status","cam":1,"state":"ok","fps":9.7,"infer_ms_p50":18.2,"dropped":12}
```

`state` es `starting`, `ok`, `reconnecting` o `disabled`. Una cámara fuera del
conjunto habilitado detiene su fuente y publica `disabled` con `fps: 0`.

## Licencia

AGPL-3.0-or-later, en correspondencia con la licencia de los pesos de
Ultralytics utilizados por el servicio.

