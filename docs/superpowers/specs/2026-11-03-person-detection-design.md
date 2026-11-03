# Detección de personas y seguimiento PTZ (POC)

Estado: borrador para revisión del propietario. Fecha: 2026-11-03.

## Objetivo

En tiempo real y por cámara:
- detectar personas;
- avisar cuando alguien pisa una zona dibujada (por ejemplo, la puerta);
- hacer zoom sobre quien entra en una zona;
- seguir con el motor PTZ a cualquier persona detectada, aunque esté fuera de la zona.

Cada detección deja un evento en el historial, se dibuja sobre el directo y sale por los sinks de notificación. ntfy queda preparado pero desactivado. MQTT para Home Assistant llegará en un subproyecto posterior.

Alcance del POC: la cámara 1 (puerta, PTZ) y una segunda cámara (la 3) sin PTZ. Todo es configurable por cámara; nada depende de un número de canal fijo.

## Decisiones tomadas con datos

- **Modelo:** YOLO26n end-to-end (sin NMS) a 416 px con ONNX Runtime en CPU.
  - En el Ryzen 3 3200G de Baphomet da 47 fps con un flujo y 56 fps en total con dos. Basta para dos cámaras a 10 fps con unos 1,4 núcleos.
  - Calidad en el clip diurno: precisión 0,95 y recall 0,62. El recall por frame es bajo, pero el tracker y el antirrebote lo compensan.
  - Descartados: OpenVINO (no rinde en AMD), ROCm (gfx902 sin soporte) y 640 px (no llega a tiempo real). ncnn-Vulkan queda pendiente de medir cuando el usuario esté en los grupos `render` y `video`.
- **Licencia:** AGPL-3.0-or-later en todo el repo, porque los pesos de Ultralytics la imponen.
- **Spike de PTZ** (2026-11-01):
  - El firmware no informa de la posición.
  - Los movimientos continuos se cortan solos a unos 36,5 s.
  - Empujar contra los topes **hace perder la referencia del motor**: después del spike los presets apuntaban a otros sitios, hasta que la cámara se reinició.
  - Conclusiones:
    - Nunca hacer homing contra los topes. La referencia fiable es el autochequeo de la cámara al arrancar y después el preset de inicio.
    - El seguimiento se mueve por pulsos cortos y con límites por software lejos de los topes.

## Arquitectura (sección 1, aprobada)

```
go2rtc rtsp://127.0.0.1:8554/camN_sub
        │
opennvr-detect (Python, contenedor propio, red del host, solo 127.0.0.1:8556)
  último frame → YOLO26n-e2e → ByteTrack → WS /detections
        │
opennvr-web (Go) internal/vision
  Client → Occupancy (zonas) ┐
                             ├→ Engine por cámara → camera.Guard / Preset (PTZ)
  Store (config por cámara)  ┘        │
                                      └→ Bus de eventos → historial, SSE, notify.Sink
```

- **Contrato del detector:** mensajes `detections` (cajas normalizadas 0..1 e ID de track) y `status` (estado, fps, latencia), además de `/healthz`. Está fijado en `orchestration/tasks/vision-detect.md` y lo copia `detector/README.md`.
- **El detector no toma decisiones:** solo ve y numera. Toda la lógica (zonas, PTZ, eventos) vive en Go, donde ya están el PTZ, la sesión y la web.
- **Configuración:** se guarda en `/config/vision.json` con `statefile`. Por cámara incluye: activada, zonas, seguimiento y preset de inicio.

## Pose de la cámara y motor de seguimiento (sección 2)

### Pose

Como la cámara no informa de su posición, `Pose` es lo que open-nvr sabe de ella:

| Pose | Significado | Zonas activas |
|---|---|---|
| `preset N, verificada` | Fue al preset N y la imagen coincide con la referencia de ese preset | Las ligadas al preset N y las ligadas a «cualquiera» |
| `preset N, sin verificar` | Fue al preset N y aún no hay referencia, o no se pudo comparar | Igual, con aviso en la UI |
| `desplazada` | Se movió por seguimiento o a mano, o la verificación falló | Solo las ligadas a «cualquiera» |

**Verificación visual.** Al guardar una zona se guarda también una imagen de referencia del preset: un frame en gris de 160×90. Cada vez que la cámara llega a un preset, se compara el frame actual con la referencia mediante desplazamiento global, el `estimateShift` del spike, que se mueve a `internal/vision/pose`.
- Desplazamiento ≤ 4 px: pose verificada.
- Si no: se repite el `Goto` una vez y se vuelve a comparar.
- Si sigue fallando: pose `desplazada`, evento `camera_displaced` y aviso «la cámara no está donde debería; reiníciala o vuelve a guardar el preset».

Así open-nvr **sabe** si está donde cree, sin depender de que el preset funcione.

Las referencias de día y de noche (IR) son distintas, así que se guarda una referencia por modo. El modo se detecta por la saturación del frame (en IR es casi 0).

**Arranque y reinicios.**
- Al iniciar open-nvr, al detectar que una cámara se ha reconectado (eventos 706/707 del NVR, o el detector pasa de `reconnecting` a `ok`) o al cortarse la luz, la cámara hace su propio autochequeo.
- open-nvr espera a que la imagen esté quieta 3 s, hace `Goto HomePreset` y verifica.
- Nunca hay homing contra los topes.

**Estimación por pulsos (dead reckoning).** Mientras sigue a alguien, el motor cuenta los pulsos netos por eje desde el preset. Ese contador sirve para dos cosas: poner **límites por software**, con un máximo de pulsos netos por eje configurable, y estimar la vuelta. No pretende dar una posición absoluta. Al volver siempre se hace `Goto` al preset y se verifica: el preset es lo que corrige la posición, no los pulsos.

### Máquina de estados por cámara

```
             persona (seguimiento on)            perdida > ReturnAfter
 EN_PRESET ────────────────────────► SIGUIENDO ─────────────────► VOLVIENDO
     ▲  ▲                               │  ▲                           │
     │  └──── verificación ok ──────────┼──┼───────────────────────────┘
     │                                  │  └ persona reaparece
     │            el usuario mueve el PTZ desde la web
     └───────────── PAUSADO (ReturnAfter sin uso manual) ◄────── cualquier estado
```

- **EN_PRESET:** zonas activas según la pose. Si alguien entra en una zona con `Alert`, se genera un evento `zone_enter`. Si la zona tiene `Zoom` y la cámara tiene seguimiento, pasa a SIGUIENDO con zoom.
- **SIGUIENDO:**
  - Objetivo: un único track, el primero que entró en una zona o, si no hay ninguno, el de mayor confianza. Se mantiene mientras exista, aunque aparezcan otros.
  - Control por pulsos de 250 ms a la velocidad configurada (`Guard.Start` y luego `Stop`). Tras cada pulso se descartan 3 frames, porque la imagen llega con retraso.
  - Zona muerta: el 15 % central en cada eje.
  - Zoom solo si la entrada vino de una zona con `Zoom`: acerca hasta que la persona ocupe `ZoomTargetHeight` (por defecto 0,5) de la altura del frame y no acerca más si la caja toca un borde.
  - Si se alcanza el límite por software, deja de mover en ese eje.
- **VOLVIENDO:** quita el zoom, hace `Goto HomePreset` y verifica. Al terminar vuelve a EN_PRESET y la pose queda verificada o desplazada.
- **PAUSADO:** cualquier orden PTZ manual desde la web gana siempre. El motor se aparta y no mueve la cámara hasta pasar `ReturnAfter` sin uso manual; entonces pasa a VOLVIENDO.
- **Antirrebote y enfriamiento:**
  - Entrada en zona tras 3 frames y salida tras 2 s, como hace `Occupancy`.
  - Como mucho un `zone_enter` por zona y track.
  - Enfriamiento de notificación de 60 s por zona.

**Cámaras sin PTZ:** solo usan zonas y eventos; su pose es siempre «cualquiera».

## Eventos, historial y notificaciones (sección 3)

- Tipos de `Event`:
  - `zone_enter`, `zone_leave`;
  - `tracking_start`, `tracking_stop`;
  - `camera_displaced`, `detector_down`.
- Cada evento lleva: cámara, zona, track, hora y la caja en coordenadas normalizadas.
- `zone_enter` y `tracking_start` guardan una instantánea JPEG desde go2rtc (`/api/frame.jpeg?src=camN`).
- Historial en `/data/vision/events.jsonl` con las instantáneas al lado, y retención por días configurable (por defecto 14).
- Un bus con varios suscriptores reparte cada evento al historial, a las SSE de la web y a cada `notify.Sink`.
  - `notify.Sink` es una interfaz con `Send(ctx, Event) error`.
  - El sink de ntfy reutiliza el `internal/notify` actual y su límite de frecuencia. Está desactivado mientras no se configure.
  - El sink MQTT (Home Assistant) se añadirá en el subproyecto HA.
- **Vigilante de flujos:** si el detector informa `fps = 0` durante 15 s con la cámara en línea, o go2rtc tiene un productor de vídeo sin bytes, se emite `detector_down` y se fuerza a go2rtc a reconectar ese flujo. Es el caso de la cámara 3 del 2026-11-01, que tenía audio pero no vídeo.

## Interfaz web (sección 4)

- **Overlay en el directo:** un canvas sobre el vídeo dibuja las cajas con su ID, las zonas activas, el estado del motor y la pose. Los datos llegan por SSE desde `/api/vision/{ch}/stream`, con detecciones a 10 Hz y eventos.
- **Editor de zonas:**
  - Se abre desde el directo de la cámara.
  - Congela una instantánea de la posición actual. El usuario elige el preset al que se liga la zona («cualquiera» o el preset N, y la cámara va a ese preset), hace clic para poner puntos y doble clic para cerrar.
  - Opciones de la zona: nombre, `Alert` y `Zoom`.
  - Al guardar, se guardan la zona y la imagen de referencia del preset.
- **Ajustes por cámara:** visión activada, seguimiento activado, velocidad, altura objetivo del zoom, `ReturnAfter`, preset de inicio y límites de pulsos.
- **Historial:** lista de eventos con su instantánea y filtros por cámara y tipo.

## Despliegue (sección 5)

- Servicio `detector` en `deploy/baphomet/compose.yaml`:
  - red del host y escucha solo en `127.0.0.1:8556`, sin puertos publicados;
  - `read_only`, `cap_drop: ALL` y usuario sin privilegios;
  - modelo montado desde un volumen, no incluido en la imagen.
- Imagen `ghcr.io/belcaik/open-nvr-detect`. El ONNX se exporta una vez con `bench.py export --e2e` y se copia al volumen.
- `opennvr-web` lee la URL del detector de `VISION_DETECT_URL` y la ruta de configuración de `VISION_CONFIG`. Si el detector no está disponible, la web funciona igual y muestra «detector no disponible».

## Errores y degradación

- **Detector caído:** el cliente reconecta con backoff, el overlay muestra el estado y el motor vuelve al preset.
- **Fallo de PTZ:** reintenta `Stop` 3 veces (es lo que ya hace `Guard`), registra el fallo y pasa a PAUSADO.
- **Verificación fallida:** pose `desplazada`, aviso y zonas ligadas a preset inactivas. Nunca se dispara un falso «hay alguien en la puerta» con la cámara mirando a otro sitio.
- **Configuración inválida:** el endpoint devuelve 400 con el campo que falla y la configuración guardada no cambia.

## Pruebas

- **Python:** letterbox y decode, ID estable con ByteTrack en el clip real, contrato del WebSocket, clientes lentos y reconexión de la fuente.
- **Go `internal/vision`:** protocolo, zonas (punto de pies), ocupación con antirrebote, `Store` y cliente WebSocket.
- **Motor:** máquina de estados con dobles de reloj, PTZ y frames. Se prueba que:
  - cada pulso termina en `Stop`;
  - se respetan los límites;
  - lo manual gana siempre;
  - al volver siempre se verifica.
- **Prueba real:** en la cámara 1, que el usuario cruce la zona de la puerta. Criterio: llega el evento con su instantánea, la cámara sigue y hace zoom, y vuelve al preset 1 con la pose verificada.

## Fuera del POC

- MQTT y Home Assistant (subproyecto siguiente, con Mosquitto en el compose).
- Configuración real de ntfy.
- ncnn-Vulkan.
- Posición absoluta por homografía y panorámica.
- Varias personas objetivo a la vez.
