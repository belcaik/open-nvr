# open-nvr

Integración local en Linux del kit de videovigilancia **2NLF GAD3035WT2**
(un NVR con software EasyVideo y cuatro cámaras PoE), para usarlo sin la
aplicación de Windows ni servicios en la nube.

El equipo es del autor. Todo el trabajo es local y de interoperabilidad.

## Estado

Etapas 0–2 completadas y etapa 3 (auditoría) documentada. La etapa 4
(monitoreo en Baphomet) está implementada pero sin desplegar.

| Función | Estado |
|---|---|
| Vídeo en directo (RTSP HEVC 2304×1296 y 800×448) | ✅ |
| Audio de la cámara (G.711 μ-law) | ✅ |
| Pan/tilt, zoom (hasta 6.8X), presets, luz | ✅ Por API HTTP propietaria |
| Captura JPEG y eventos por WebSocket | ✅ |
| ONVIF | ❌ El NVR no lo ofrece |
| Audio bidireccional | 🟡 Protocolo P6S documentado ([p6s-protocol.md](docs/p6s-protocol.md)); falta la fórmula del login |
| Grabación | ⚠️ Disco sin formatear y grabación detenida |
| Cámaras 2–4 | ❓ Solo hay una cámara registrada |
| Salida a Internet bloqueada | ⏳ Preparada en `deploy/baphomet/`, sin desplegar |

Leyenda: ✅ comprobado · ❌ no disponible · ⚠️ requiere acción · ❓ pendiente ·
⏳ en curso.

## Prueba rápida

```bash
read -rs NVR_PASS; export NVR_USER=admin NVR_PASS   # ajustar usuario
mpv --rtsp-transport=tcp "rtsp://$NVR_USER:$NVR_PASS@192.168.0.227:554/live/0/MAIN"
```

## Documentación

- [docs/hardware.md](docs/hardware.md): inventario, servicios y flujos, con lo
  comprobado separado de lo pendiente y comandos para reproducirlo.
- [docs/protocols.md](docs/protocols.md): RTSP, ONVIF (PTZ, Imaging,
  backchannel de audio), lo que dice la documentación pública del fabricante y
  las herramientas disponibles en Arch Linux.
- [docs/plan.md](docs/plan.md): pruebas, arquitectura y etapas.
- [docs/test-log.md](docs/test-log.md): registro de pruebas con resultados y evidencia.
- [docs/api-reference.md](docs/api-reference.md): API HTTP/WebSocket del NVR.
- [docs/security.md](docs/security.md): hallazgos de seguridad clasificados.
- [docs/monitoring.md](docs/monitoring.md) y [deploy/baphomet/](deploy/baphomet/README.md):
  monitoreo y bloqueo de salida en Baphomet.

## Arquitectura prevista

go2rtc es el único cliente RTSP del NVR y reparte los flujos a mpv y al
navegador. El control (PTZ, luz, estado) usa la API HTTP propietaria, porque
el NVR no implementa ONVIF. Baphomet hace de puerta de enlace del NVR: bloquea
la salida a Internet por defecto y avisa por ntfy de destinos nuevos y de
cambios de comportamiento.
El detalle está en la [etapa 4 del plan](docs/plan.md#etapa-4-arquitectura-local-mínima).

## Reglas de trabajo

1. **Autorización del propietario (2026-10-26):** se permiten el diagnóstico,
   PTZ, audio, cambios reversibles de configuración, reinicios controlados y
   capturas de tráfico propio. Requieren autorización específica: firmware,
   bootloader o particiones, procedimientos con riesgo de inutilizar el
   equipo, borrar grabaciones o formatear, restablecer de fábrica y exponer
   el equipo a Internet. Las pruebas se clasifican
   en pasivas, de lectura, de actuación y de cambio
   ([clases](docs/plan.md#clasificación-de-las-pruebas)).
2. **Hechos, hipótesis y pendientes** se marcan siempre por separado. Un dato
   pasa a ✅ solo con un comando reproducible y su resultado.
3. **Credenciales** solo en variables de entorno. Nunca en el repositorio,
   en los logs ni en las capturas versionadas.
4. **Capturas** (`captures/`) fuera de git: contienen números de serie, MAC y
   a veces credenciales.
5. El NVR (servidor Boa 0.94.14rc21, sin mantenimiento) **no se expone a
   Internet**. El acceso remoto se hace por VPN al host Linux.
