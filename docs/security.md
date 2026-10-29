# Hallazgos de seguridad

Auditoría del NVR `N310B-11NEA-AI-Z02-P4` (firmware 6.9.2-20251226) y de su
cámara `W3CA-52PHM-S/W/BT` (firmware 1.8.1-20260112), hecha el 2026-10-26/27.

**Evidencia:**

- **Verificado**: observado en el equipo con una petición de solo lectura o
  con el uso normal de la API; la evidencia está en `captures/`.
- **Público**: lo dice una fuente pública sobre el componente, pero no se ha
  comprobado en este equipo.
- **Hipótesis**: inferencia sin verificar.

**Gravedad** (en el contexto de una LAN doméstica): Alta, Media o Baja.

No se ejecutaron pruebas de explotación, de denegación de servicio ni de
fuerza bruta. **Ninguna CVE se atribuye a este equipo**: las CVE públicas de
sus componentes figuran como contexto, separadas de los hallazgos.

## Hallazgos verificados

| ID | Hallazgo | Gravedad | Evidencia |
|---|---|---|---|
| S01 | **[Mitigado el 2026-10-27: sin salida a Internet, P2P `offline` (T64)]** **P2P TUTK activo y conectado a Internet.** `GET /Network/P2PV2`: `P2PProtocolName=tutk`, `Enable=true`, `Status=internet-ready`. El NVR es accesible desde la plataforma P2P del fabricante con su UID, sin pasar por la red local | Alta | `captures/api/Network_P2PV2.xml` |
| S02 | **Credenciales sin cifrar.** La API HTTP usa Basic auth sin TLS y RTSP también acepta Basic. Cualquier equipo de la LAN que vea el tráfico obtiene la contraseña de administrador | Alta | `test-log.md` T02b y T02c |
| S03 | **La cámara también tiene TUTK** (`TutkEnable=true`, con DID propio). Ahora está `offline` porque el segmento PoE aislado no tiene salida | Media | `captures/api/System_FrontConnectDevicetList.xml` |
| S04 | **Contraseña débil en las cuatro cámaras.** El NVR informa `LoginStatus=weak password` en los canales 1–4. La edición `PUT /System/FrontConnectDevicetList` sólo demuestra cambiar la credencial que el NVR guarda para conectarse; no se encontró una operación que cambie la contraseña dentro de la IPC. Hace falta acceso directo temporal o una vía de passthrough verificada | Media | `captures/api/System_FrontConnectDeviceStatusList.xml`; análisis estático `rea-ipc-catalog-codex` |
| S05 | **Autenticación del WebSocket sin nonce del servidor**: `md5(contraseña + epoch_ms)` en claro por `ws://`. El servidor la aceptó (`code 0`). Clasificación genérica: CWE-294 (replay) y CWE-916 (un token capturado permite atacar la contraseña offline). No se midió la ventana de replay | Media | `captures/T30-ws-events.txt`, `docs/api-reference.md` §3 |
| S06 | **Servidor web Boa 0.94.14rc21**, sin mantenimiento desde 2005 | Media (por exposición, no por un fallo comprobado) | Banner HTTP |
| S07 | **Rutas a servicios en la nube** en la UI web: `erp.zwcloud.wang:8090` (WASM y paquetes), `utilerp.p6sai.com:8090` (actualización cloud), `139.9.6.140:8090` (comentada). El NVR declara además DNS `1.1.1.1`/`8.8.8.8` y NTP `pool.ntp.org` | Media | `docs/api-reference.md` §7, `captures/api/Network_Interfaces.xml` |
| S08 | **[Resuelto el 2026-10-27]** Zona horaria `CST-08:00` (China) y NTP fallido. Ahora UTC−3 y NTP servido por Baphomet (T67) | Resuelto | `captures/api/System_NTP.xml` |
| S09 | ~~Grabación detenida y disco sin formatear~~ **Resuelto el 2026-10-27**: disco formateado y grabación continua confirmada (T40). Los campos `RecordMode=stop` y `DiskFormatStatus=unformatted` no reflejan el estado real | Resuelto | `captures/T40-*` |
| S10 | **Puertos sin identificar**: 8885/tcp y 9000/tcp aceptan conexión pero no responden a HTTP ni envían banner. 6060 y 6066 son el protocolo privado P6S | Baja (superficie no auditada) | `test-log.md` T01–T02 |
| S11 | **La UI guarda `Base64(usuario:contraseña)` en la cookie `userInfo`** y, en modo P2P, pasa las credenciales en la URL | Baja (afecta al navegador del cliente) | `docs/api-reference.md` §8 |
| S12 | **Detección de movimiento desactivada** en el canal 1: no se generan eventos | Baja (funcional) | `captures/api/Pictures_1_Motion.xml` |
| S14 | **Activador oculto de telnet en la UI web**: 5 clics en la etiqueta "MAC" de la pantalla de red en menos de 2 s ejecutan `openTelnet()`, que envía `PUT /System/DeviceCustomFunction` con el cuerpo `telnetd_enable` (`nvr/js/network/ipAddress.js`). **No se ejecutó.** El puerto 23 está cerrado (T01). Cualquiera con la contraseña de administrador podría abrir una consola telnet en el NVR. `nvr-watch` vigila 23/tcp y 2323/tcp | Alta (verificado en el código, no en el equipo) | `captures/web/nvr/js/network/ipAddress.js` |
| S15 | **El cliente EasyVideo consulta actualizaciones por HTTP sin cifrar** en `utilerp.p6sai.com:8090` (`112.74.43.252`) y usa un bucket en Huawei Cloud. El análisis estático encontró además un worker que reintenta cada 600 000 ms; el instalador auxiliar no valida criptográficamente el paquete en el camino analizado. Un atacante en el camino podría manipular la respuesta. No se ha observado si el NVR hace lo mismo (pendiente del monitoreo en Baphomet) | Media | `captures/vm/http-requests.txt`; análisis estático `rea-ipc-catalog-codex` |
| S16 | **El login P6S envía en claro usuario, IP y MAC del cliente**. Deriva `LoginFlag` cifrando un reto con DES o 3DES usando la contraseña como clave, pero todo el protocolo, incluido el audio, viaja sin cifrar | Baja | [p6s-protocol.md](p6s-protocol.md) |
| S19 | **`VideoNetClient_UserLogin` registra la contraseña en texto claro** mediante `log("Username: %s Password: %s\n", user, pass)` antes de llamar a `CUser::Login`. No se inspeccionaron los logs de la VM ni se copiaron credenciales; la llamada y el formato están en el DLL analizado | Alta si esos logs son accesibles | `VideoNetClient.dll` VA `0x10073921` y cadena VA `0x1017cfb8`; análisis estático `p6s-login-wrapper` |
| S17 | **Destinos fijos en el firmware** (observados en Baphomet, todos bloqueados, sin ninguna consulta DNS previa): `110.41.13.246` (Huawei Cloud, Cantón), `164.152.106.227` (Oracle, Chicago) y `190.92.209.173` (Huawei Cloud, Singapur) en **32100/udp**, unos 1100 paquetes cada uno en ~15 min; `47.106.167.176:8090/tcp` (Alibaba Cloud, Shenzhen; mismo puerto que la nube de actualizaciones `UtilERP`). Con IP fijas, un bloqueo por DNS no serviría: hace falta el filtrado por IP de Baphomet | Media (mitigado) | `egresswatch summary`, contadores nft |
| S18 | **El P2P se identifica como `tutk`, pero tiene aspecto de PPPP de CS2 Network** (iLnkP2P y similares): servidores en 32100/udp e identificador con formato `XXXXXX-NNNNNN-XXXXX`, típico de PPPP. Si fuera así, las CVE relevantes serían las de esa familia (por ejemplo CVE-2019-11219, enumeración de dispositivos) en lugar de las de Kalay. **Hipótesis**: no se ha analizado el protocolo | Media (hipótesis; mitigado por el bloqueo) | S17, `captures/api/Network_P2PV2.xml` |
| S13 | **Riesgo en la red de gestión**: mientras Baphomet estuvo apagado, otro equipo con una clave SSH distinta respondió en 192.168.0.8. SSH lo detectó y no se envió nada. Conviene reservar esa IP en el DHCP del router | Media | Sesión del 2026-10-26 |

### Lo que está bien

- La API exige autenticación: sin credenciales devuelve `401`.
- El segmento PoE está aislado de la LAN: la cámara no responde a ARP ni a ping
  desde 192.168.0.0/24.
- No hay ONVIF ni WS-Discovery activos, así que la superficie SOAP es nula.
- DDNS, e-mail y FTP están desactivados.
- El análisis estático no encontró credenciales fijas en la UI web.

## Contexto público de los componentes

Fuentes en la investigación del agente `research-security`; la síntesis está
aquí.

- **Boa 0.94.14rc21**: NVD lista varias CVE contra esta versión (por ejemplo
  CVE-2018-21027 y CVE-2018-21028, de denegación de servicio, y
  CVE-2024-47916, de path traversal, que el CNA marca como "todas las
  versiones" porque Boa no tiene mantenimiento). Otras están disputadas
  porque afectan a CGI del integrador, no a Boa. **No se ha verificado si
  alguna aplica a este firmware.** La API real la sirve un componente propio
  (`CGI/1.6.3`) del que no hay información pública.
- **TUTK Kalay**: CVE-2021-28372 (suplantación de dispositivos conociendo su
  UID) y CVE-2021-32934 (protección débil de datos en SDK antiguos), más las
  CVE-2023-6321 a 6324 de Bitdefender. La mitigación oficial es un SDK
  actualizado con AuthKey y DTLS. **Se desconoce la versión del SDK de este
  firmware.** Fuente: [CISA ICSA-21-229-01](https://www.cisa.gov/news-events/ics-advisories/icsa-21-229-01).
- **RtpRtspFlyer** y el ecosistema **P6S / zwcloud / p6sai**: no hay avisos
  públicos. `139.9.6.140` es una VM de Huawei Cloud en Guangzhou y
  `p6sai.com` está registrado a través de Alibaba Cloud. Que no haya reportes
  no equivale a que sea seguro.

## Recomendaciones

Ordenadas por impacto. Las marcadas con ⚙ cambian la configuración del NVR
o de la red. Son reversibles y entran en la autorización vigente, pero no se
han aplicado todavía.

1. ⚙ **Cortar la salida a Internet del NVR por defecto** con Baphomet como
   puerta de enlace y una política de descarte (etapa 4,
   [monitoring.md](monitoring.md)). Así se neutralizan S01, S03 y S07 sin
   depender del firmware.
2. ⚙ **Desactivar P2P** (`PUT /Network/P2PV2` con `Enable=false`) si no se
   usa la app móvil. Es complementario al punto 1.
3. ⚙ **Cambiar la contraseña del NVR** al acabar la investigación (ya
   previsto) y **la de la cámara** (S04). Una contraseña distinta para cada
   uno.
4. Acceder a la interfaz web y a RTSP solo desde la LAN o por VPN. Nunca
   reenviar puertos en el router.
5. ⚙ Zona horaria local y NTP hacia Baphomet o un servidor permitido (S08).
6. Reservar en el DHCP del router las IP de Baphomet (192.168.0.8) y del NVR
   (S13).
7. Hecho: disco formateado y grabación activa (S09).
9. No usar el activador de telnet (S14). Si el puerto 23 llega a abrirse,
   tratarlo como un incidente: cambiar la contraseña y reiniciar el NVR.
8. Pendiente: identificar 8885 y 9000 con captura pasiva del cliente oficial
   (S10).
