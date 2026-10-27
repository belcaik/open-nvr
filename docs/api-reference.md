> Generado por análisis estático (agente Codex, run `api-analysis`, 2026-10-26) de la copia de la UI web en `captures/web/` y revisado contra el código. Las rutas `web/...` se refieren a `captures/web/...`. La verificación en el dispositivo real está en [test-log.md](test-log.md).

# Referencia estática de la API HTTP del NVR

## Alcance, convenciones y límites

Este documento se obtuvo **exclusivamente por análisis estático** de `./web`; no se contactó al equipo ni a ningún host. La base normal es `config.baseUrl = location.protocol + "//" + location.hostname + ":" + puerto` (`web/js/components/system.config.js:2-9`). En modo P2P, el envoltorio de AJAX transforma cada llamada en `GET /mixinGet?url=...&id={p2pId}`, `POST /mixinPost?...`, `PUT /mixinPut?...` o `DELETE /mixinDelete?...`; las respuestas XML llegan en Base64 dentro de `data[0].attach.response` (`web/js/components/ajax.ext.js:43-64,107-125`).

Convenciones:

- `{ch}` es un canal HTTP contado desde **1**. En varios mensajes del reproductor local, en cambio, `channel` es cero-based; se indica donde corresponde.
- `{stream}` vale `1` (principal), `2` (substream) o `3` (tercer stream).
- “XML devuelto” significa que el UI conserva el documento GET, cambia los nodos indicados y lo vuelve a enviar por PUT; los nodos no visibles no pueden enumerarse sin una respuesta real.
- La UI inicia sesión con `PUT /Security/UserAuth` y también provoca HTTP Basic mediante una petición jQuery con `username`/`password`; guarda `Base64(usuario:contraseña)` en la cookie `userInfo`, que **no es cifrado** (`web/js/pages/login.js:244-262,311-330`). Algunas llamadas AI agregan el encabezado `TOKEN` recibido al autenticar (`web/js/pages/preview.js:4304-4306`).

Clases: **READ** = GET sin efecto lateral aparente; **WRITE-REVERSIBLE** = cambia configuración/estado normalmente reversible; **DANGEROUS** = credenciales/usuarios, borrado, formato, restauración, reinicio o firmware. Acciones como probar correo/FTP o mover PTZ se marcan WRITE-REVERSIBLE aunque producen una acción física/red.

## Catálogo completo de endpoints HTTP

### Autenticación, inventario y capacidades

| Método y ruta | Solicitud | Elementos de respuesta leídos | Clase | Uso |
|---|---|---|---|---|
| `PUT /Security/UserAuth` | Form URL-encoded: `Username`, `Password` | `ResponseStatus/statusCode`; header `Token` | WRITE-REVERSIBLE (autenticación; no se observa cambio persistente) | `web/js/pages/login.js:244-260`; `web/nvr/js/common.js:545-563` |
| `GET /System/DeviceCap` | — | `StreamCount`; capacidades bajo `Device`, `FunctionList`, `FunctionListAboutChannel/ChannelList/Channel`, `AIOverlay`, `DetectRegion`, `FaceFunction`, `RejectEmptyPassword`, `Protocol`, `Motion`, `PrivacyZone`, `PlaybackSpeed`, `PTZ_Advance_Cruise`, `NtpTimeSync` y sus hijos | READ | `web/nvr/js/common.js:8-126` |
| `GET /System/DeviceCapV2` | — | lista `FunctionListAboutChannel/ChannelList/Channel`; los mismos grupos por canal, además `ExposureTime`, `AntiFlickerMode`, `MatrixOSD` | READ | `web/nvr/js/common.js:125-253` |
| `GET /System/DeviceCap/{ch}` | — | documento de capacidades usado por la pantalla de máscara; no se leen nodos en el callback | READ | `web/nvr/js/image/privacySttings.js:314-327` |
| `GET /System/AICap` | — | `SmartDetect/{PassengerFlowStatisticsQuery,ElectronicFence,TraversePlane,ObjectTrack,PassengerFlowStatisticsOverlay}`, `AudioAlarmType/{ForCar,ForHuman}` | READ | `web/nvr/js/common.js:340-454` |
| `GET /System/DeviceInfo` | — | `ProductType`, `DeviceTypeName`, `DeviceName`, `SerialNum`, `SoftWareVersion`, `VideoInputChannels`, `AudioChannels`, `TwoWayAudio`, `AlarmInputChannels`, `AlarmOutputChannels` | READ | `web/nvr/js/common.js:470-510` |
| `GET /System/DeviceStatus/{ch}` | — | `MainStreamVideoBitrate`, `FirstSubStreamVideoBitrate`, `SecondSubStreamVideoBitrate` | READ | `web/nvr/js/common.js:522-540` |
| `GET /System/DeviceCustomFunction` | — | `DeviceCustomFunction/NetWork/Lan/DHCPEnable` | READ | `web/nvr/js/network/ipAddress.js:47-61` |
| `GET /System/PTZCap` | — | primer `Channel/Extend/Support`, `Light/Support`, `Wipe/Support` | READ | `web/js/pages/preview.js:3139-3168` |
| `GET /System/LightCap/{ch}` | — | `LightCapability/LightMode` y capacidades/rangos de modos color, inteligente, IR variable, blanco variable, IR normal y blanco normal (`Support`, `Min/Max`, `OpenLight`, `CloseLight`, etc.) | READ | `web/nvr/js/image/lighting.js:1641-2041` |
| `GET /Protocol.html` | — | texto entre `Name0=`, `Type0=` y `Name1=`; la UI añade además protocolo `6 = ONVIF` | READ | `web/nvr/js/channelManagement/videoManagement.js:267-283` |

### Red, nube y servicios

| Método y ruta | Solicitud | Respuesta usada | Clase | Uso |
|---|---|---|---|---|
| `GET /Network/Interfaces` | — | `InterfaceList/Interface`: `ID`, `AddressingType`, `IPAddress`, `SubnetMask`, `Gateway/IPAddress`, `PrimaryDNS`, `SecondaryDNS`, `MacAddress` | READ | `web/nvr/js/network/ipAddress.js:66-116`; `web/nvr/js/common.js:271-288` |
| `PUT /Network/Interfaces` | XML devuelto, modificando los nodos anteriores | estado HTTP mediante `SaveState` | WRITE-REVERSIBLE | `web/nvr/js/network/ipAddress.js:118-141` |
| `GET /Network/Port` | — | `Port/{HTTP,Command,Media,RTP}` | READ | `web/nvr/js/network/port.js:14-32`; `web/nvr/js/common.js:295-312`; `web/js/pages/login.js:311-330` |
| `PUT /Network/Port` | XML devuelto `Port` con `HTTP`, `Command`, `Media`, `RTP` | estado HTTP | WRITE-REVERSIBLE | `web/nvr/js/network/port.js:36-59` |
| `GET /Network/P2PV2` | — | `P2PList/P2P/{Enable,UUID,State,DidGetStatus,...}` | READ | `web/nvr/js/network/p2p.js:84-133,147-225`; `web/nvr/js/common.js:316-334` |
| `PUT /Network/P2PV2` | XML devuelto, al menos `P2P/Enable` | estado HTTP | WRITE-REVERSIBLE | `web/nvr/js/network/p2p.js:228-250` |
| `PUT /System/ApplyDIDManul` | `<ApplyDIDManulCfg><RequestUrl/></ApplyDIDManulCfg>` | no inspeccionada | WRITE-REVERSIBLE | `web/nvr/js/network/p2p.js:52-64` |
| `GET/PUT /System/ActiveRegistCfgUIDesign` | GET conserva XML; PUT reenvía el documento editado (campos exactos dependen de la respuesta) | GET llena controles mediante XML; PUT usa estado HTTP | WRITE-REVERSIBLE para PUT / READ para GET | `web/nvr/js/network/Proactively.js:15-51` |
| `GET /Network/DDNS/Config` | — | `Config/{Enable,ProtocolName,DomainName,UserName,RefreshIntervals}` | READ | `web/nvr/js/network/ddns.js:35-52` |
| `PUT /Network/DDNS/Config` | `<Config><Enable/><ProtocolName/><DomainName/><UserName/><Password/><RefreshIntervals/></Config>` | estado HTTP | WRITE-REVERSIBLE | `web/nvr/js/network/ddns.js:56-76` |
| `GET /Network/DDNS/Status` | — | `Status/State` | READ | `web/nvr/js/network/ddns.js:80-100` |
| `GET /Network/DDNS/ServerList` | — | `ServerList/ServerName[]` | READ | `web/nvr/js/network/ddns.js:105-120` |
| `GET/PUT /Network/EMail` | PUT `<EMail>`: `Enable`, `SMTPServer`, `SMTPPort`, `SenderEmailAddress`, `SenderEmailPassword`, `AttachedSnapshot`, `Interval`, `Authentication`, `UserName`, `Password`, `Subject`, `Message`, `SMTPTLSConfig/SMTPTLS`, `ReceiverList/...` | GET lee esos mismos nodos | WRITE-REVERSIBLE para PUT / READ para GET | `web/nvr/js/network/platformAccess.js:316-365,508-535` |
| `PUT /System/SendTestEmail` | cuerpo vacío | `ResponseStatus/statusCode` | WRITE-REVERSIBLE | `web/nvr/js/network/platformAccess.js:539-558` |
| `GET/PUT /Network/ftp` | PUT `<FTP><Enable/><UserName/>[<Password/>]<IPAddress/><Port/><TransferContentTypeMask/><FTPMode/><StorageDirectory/><Status/></FTP>` | GET lee esos nodos | WRITE-REVERSIBLE para PUT / READ para GET | `web/nvr/js/network/platformAccess.js:594-640` |
| `PUT /System/SendTestFTP` | vacío | `statusCode` | WRITE-REVERSIBLE | `web/nvr/js/network/platformAccess.js:562-580` |
| `GET/PUT /System/1/GB28181Config` | `<GB28181Cfg><ServerCfg><ServerID/><ServerRegion/><ServerIpAddress/><ServerPort/><UserName/><UserAuthID/><Password/><TransportLayType/></ServerCfg><AlarmInInfoList><AlarmInInfo><ID/><AlarmInID/></AlarmInInfo>…</AlarmInInfoList><VideoOutInfoList><VideoOutInfo><ID/><VideoOutID/></VideoOutInfo>…</VideoOutInfoList><AudioOutInfoList><AudioOutInfo><ID/><AudioOutID/></AudioOutInfo>…</AudioOutInfoList><Enable/><ProtocolVersion/><HeartbeatInterval/><MaxHeartbeatTimeoutCount/><RegisterExpire/><RegisterInterval/><RegisterState/><StreamType/></GB28181Cfg>` | GET lee los mismos campos | WRITE-REVERSIBLE para PUT / READ para GET | `web/nvr/js/network/platformAccess.js:405-448,656-719` |

### Vídeo, audio, imagen y OSD

| Método y ruta | Solicitud | Respuesta usada | Clase | Uso |
|---|---|---|---|---|
| `GET /Streams/{ch}/CapabilityV2` | — | `StreamCapability/MainStreamCapabilityList/MainStreamCapability`, resoluciones, `MaxFrameRate`, listas de substream y `CodecCapabilityList/streamCodecCapability/Codec` | READ | `web/nvr/js/audioAndvideo/video.js:184-258`; `web/js/pages/preview.js:3348-3370` |
| `GET/PUT /Streams/{ch}/{stream}` | PUT `<StreamConfig><ID/><StreamType/><VideoCodecType/><ResolutionWidth/><ResolutionHeigth/><FrameRate/><BitrateType/><Bitrate/><FixedQuality/><keyFrameInterval/><Transport><RTSPURI/></Transport></StreamConfig>`; la variante de preview modifica el XML GET | GET lee esos nodos | WRITE-REVERSIBLE para PUT / READ para GET | `web/nvr/js/audioAndvideo/video.js:289-471`; `web/js/pages/preview.js:3376-3787` |
| `GET/PUT /Streams/AudioStream` | XML devuelto `<AudioStream>` modificando `AudioInMethod`, `AudioInCodec`, `AudioInVolume`, `AudioOutVolume` | los mismos cuatro nodos | WRITE-REVERSIBLE para PUT / READ para GET | `web/nvr/js/audioAndvideo/audio.js:77-122` |
| `GET/PUT /Images/{ch}/IrCutFilter` | PUT `<IrCutFillter>` con `Mode`, `DayStartTime`, `DayEndTime`, `Sensitivity`, `SwitchTime/InitiveSwitchTime/PassivitySwitchTime`, `ImageMode`, `IrCutReverse`, horarios/modos/brillo/umbrales IR y luz blanca, y parámetros color/inteligentes | GET lee esos mismos nodos | WRITE-REVERSIBLE para PUT / READ para GET | `web/nvr/js/image/lighting.js:1029-1292`; `web/nvr/js/image/ispPage.js:681-851`; versión rápida IPC `web/js/pages/preview.js:3259-3319` |
| `PUT /Images/{ch}/Restore` | `<IrCutFillter><Mode>initiative</Mode><DayStartTime>07:00:00</DayStartTime><DayEndTime>19:00:00</DayEndTime><Sensitivity>50</Sensitivity><SwitchTime>3</SwitchTime></IrCutFillter>` | estado HTTP | WRITE-REVERSIBLE | `web/nvr/js/image/ispPage.js:379-395`; `web/nvr/js/image/lighting.js:398-414` |
| `GET/PUT /Images/{ch}/Color` | `<Color><Mode>manual</Mode><Brightness/><Contrast/><Saturation/><Hue/><Sharpness/></Color>` | GET lee esos nodos | WRITE-REVERSIBLE para PUT / READ para GET | `web/nvr/js/image/ispPage.js:1258-1316`; `web/nvr/js/image/lighting.js:1534-1593` |
| `GET/PUT /Images/{ch}/Basic` | `<Basic>` con `powerLineFrequencyMode`, `NoiseReduce`, `BLC`, `WDR`, `FlipMode` y, según capacidad, intensidad anti-flicker | GET lee esos nodos | WRITE-REVERSIBLE para PUT / READ para GET | `web/nvr/js/image/ispPage.js:1325-1390`; preview `web/js/pages/preview.js:3281-3340` |
| `GET/PUT /Images/{ch}/WhiteBalance` | `<WhiteBlance><Mode/><GainRed/><GainBlue/></WhiteBlance>` (ortografía literal) | mismos nodos | WRITE-REVERSIBLE para PUT / READ para GET | `web/nvr/js/image/ispPage.js:1405-1449` |
| `GET/PUT /Images/{ch}/Exposure` | `<Exposure><IrisType/><shutter/><Gain/><Shuttermode/></Exposure>` | mismos nodos | WRITE-REVERSIBLE para PUT / READ para GET | `web/nvr/js/image/ispPage.js:1545-1579` |
| `GET/PUT /Images/{ch}/Scene` | `<Scene><Support/><Mode/><LDC/><Fog/><FogValue/><Gamma/></Scene>` | `Scene/{FogValue,...}` | WRITE-REVERSIBLE para PUT / READ para GET | `web/nvr/js/image/ispPage.js:1586-1623` |
| `GET/PUT /Pictures/{ch}/Motion` | XML GET conservado; PUT modifica los controles de `Motion` (estructura completa dependiente de respuesta) | documento `Motion` | WRITE-REVERSIBLE para PUT / READ para GET | `web/nvr/js/image/ispPage.js:1630-1661`; `web/nvr/js/image/lighting.js:1603-1634` |
| `GET/PUT /Pictures/{ch}/Mosaic` | `<Mosaic><Enable/><MosaicRegionList><MosaicRegion>…</MosaicRegion></MosaicRegionList></Mosaic>`; regiones contienen los nodos generados por el editor | GET lee `Enable` y regiones; PUT estado HTTP | WRITE-REVERSIBLE normalmente; **DANGEROUS** cuando el editor borra una/todas las regiones | `web/nvr/js/image/privacySttings.js:154-205,245-303` |
| `GET/PUT /Pictures/{ch}/OSD` | `<OSD>`; variantes escriben `DisplayName/Name` y controles de fecha/hora/posición | GET lee `OSD/DisplayName/Name` | WRITE-REVERSIBLE para PUT / READ para GET | `web/js/pages/preview.js:2472-2485,2856-2876,3067-3097`; `web/nvr/js/image/osd.js:460-486,830-855` |
| `GET/PUT /Pictures/{ch}/MultiOSDV2` | `<MultiOSD><OSD>` con `ID`, `Enable`, `Text`, posición y atributos de overlay (la UI también puede reenviar el XML GET) | `OSD/{Enable,Text,...}` | WRITE-REVERSIBLE para PUT / READ para GET | `web/js/pages/preview.js:2761-2805,2996-3031`; `web/nvr/js/image/osd.js:229-254,436-456,801-826` |
| `GET /Snapshot/1/RemoteImageCapture?ImageFormat=2` | — | imagen, usada como `src` | READ | `web/js/components/system.config.js:36`; `web/js/components/nav-menu.js:17` |
| `GET /Snapshot/1/1/RemoteImageCaptureV2?ImageFormat=2` | vía `mixinGet` P2P | imagen | READ | `web/js/components/nav-menu.js:64` |

### Hora, grabación, disco y logs

| Método y ruta | Solicitud | Respuesta usada | Clase | Uso |
|---|---|---|---|---|
| `GET/PUT /System/Time` | `<Time><DateTimeFormat/><TimeFormat/><SystemTime/></Time>` | los mismos nodos | WRITE-REVERSIBLE para PUT / READ para GET | `web/nvr/js/system/dateTime.js:356-405,497-517`; `web/js/pages/preview.js:3118-3133,3232-3249`; `web/nvr/js/image/osd.js:606-655` |
| `GET/PUT /System/NTP` | `<NTP><Enable/><ServerName/><TimeZone/><TimeSyncInterval/><LastSuccessSyncNTPTime/><IsSynchronizeNTPNow/><SynchronizeNTPNowStatus/></NTP>` | mismos nodos; respuesta de sincronización `ResponseStatus/statusCode` | WRITE-REVERSIBLE | `web/nvr/js/system/dateTime.js:303-339,410-469` |
| `GET/PUT /System/DST` | `<DST><Enable/><Mode/><TimeDifference/><DateModeStart/><DateModeStop/><WeekModeStart/><WeekModeStop/></DST>` | mismos nodos | WRITE-REVERSIBLE | `web/nvr/js/system/dateTime.js:527-617` |
| `GET /Disk` | — | `DiskList/Disk/{TotalCapacity,AvailableCapacity,DiskStorageAttribute,DiskTypeName,DiskID}` | READ | `web/nvr/js/audioAndvideo/sysinfsContrl.js:68-116` |
| `GET/PUT /Record` | PUT `<Record><RecordWriteCircleType/><RecordDeleteType/><RecordStreamType/><PreRecord/><DelayRecord/><RecordMode/></Record>` | GET lee `RecordWriteCircleType` | WRITE-REVERSIBLE | `web/nvr/js/audioAndvideo/sysinfsContrl.js:123-153` |
| `PUT /Record/Format/Call` | vacío | sólo finalización | **DANGEROUS** (formatea almacenamiento) | `web/nvr/js/audioAndvideo/sysinfsContrl.js:160-175` |
| `GET/PUT /Record/{ch}/RecordSchedule` | `<RecordSchedule><StreamType/><Schedule><AllDay/><TimeBlockList><TimeBlock_0/>…<TimeBlock_6/></TimeBlockList></Schedule></RecordSchedule>` | `Schedule/TimeBlockList/TimeBlock_0..6` | WRITE-REVERSIBLE para PUT / READ para GET | `web/nvr/js/audioAndvideo/recordPlan.js:62-117` |
| `GET /Playback/GetRecordInfoByMonth?Year={YYYY}&Month={MM}` | query | `RecordInfo` como bitmap de días | READ | `web/js/pages/remoteplayback.js:791-824` |
| `GET /Playback/GetRecordInfoByDay?Channel={ch}&RecordType=255&Year={YYYY}&Month={M}&Day={D}` | query | cada `RecordInfo`: `StartTime`, `EndTime`, `RecordType` (`general`, `manual`, `alarm`, `motion`, `ai`) | READ | `web/js/pages/remoteplayback.js:894-984` |
| `GET /System/Logging?majorType={tipo}&minorType=0&startTime={t0}&stopTime={t1}` | query | `LogList/Log/{netUser,hostIPAddress,minorType,channel,IpConflictMac,time}` | READ | `web/nvr/js/log.js:283-330` |

### Eventos, analítica y alarmas

| Método y ruta | Solicitud | Respuesta usada | Clase | Uso |
|---|---|---|---|---|
| `GET/PUT /Pictures/{ch}/VideoLost` | PUT reenvía `<VideoLost>` cambiando `Enable`, `Mail/Enable`, `BeepAlert/Enable`, `AlarmOut/Enable`, `PTZ/Enable`, `PTZ/PTZActionList/PTZAction/{ActionName,ActionNum}` y su `Schedule` | los mismos nodos | WRITE-REVERSIBLE | `web/nvr/js/events/videoLoss.js:69-86,124-165` |
| `GET /Pictures/{ch}/VideoLost/Schedule` | — | `Schedule/AllDay`, `TimeBlock_0..6` | READ | `web/nvr/js/events/videoLoss.js:91-117` |
| `GET/PUT /System/ElectronicDenceUIDesignCfg` | XML devuelto; UI cambia parámetros de cerca electrónica | documento completo guardado en `xmlDocelec` | WRITE-REVERSIBLE | `web/js/pages/preview.js:3794-3835` |
| `GET/PUT /System/CrossBorderDetectUIDesignInfo` | XML devuelto; cruce de línea | documento `xmlDoccross` | WRITE-REVERSIBLE | `web/js/pages/preview.js:3846-3886` |
| `GET/PUT /System/OffDutyDetectUIDesignCfg` | XML devuelto; abandono de puesto | documento `xmlDocoffDuty` | WRITE-REVERSIBLE | `web/js/pages/preview.js:3897-3937` |
| `GET/PUT /System/PassengerFlowStatisticsUIDesignCfg` | XML devuelto; conteo de personas | documento `xmlflow` | WRITE-REVERSIBLE | `web/js/pages/preview.js:3949-3988` |
| `GET/PUT /AI/FaceSnapshotCfg` | XML devuelto; captura facial | documento `xmlface` | WRITE-REVERSIBLE | `web/js/pages/preview.js:4003-4054` |
| `GET/PUT /FaceReco/1/RecoRuleList` | XML devuelto `RecoRuleList/RecoRule`; UI lee/escribe `Trigger/PTZ/Enable` entre otros controles | `RecoRuleList` | WRITE-REVERSIBLE | `web/js/pages/preview.js:4066-4191` |
| `GET/PUT /System/AlgorithmStoreCfg` | PUT `<AlgorithmStoreCfg><AlgorithmType>{valor}</AlgorithmType><AlgorithmStoreCfg>` (cierre defectuoso literal en JS) | GET lee `AlgorithmType`; PUT `statusCode` | WRITE-REVERSIBLE | `web/js/pages/preview.js:4220-4268` |
| `PUT /FaceGroups/QueryAll` | sin cuerpo visible; header `TOKEN` opcional | texto parseado como XML: `ResponseStatus/statusCode`, `FaceGroups/FaceGroup/{GroupName,GroupID}` | WRITE-REVERSIBLE (verbo PUT, aunque semánticamente consulta) | `web/js/pages/preview.js:4295-4324` |
| `GET /AI/CarPlateSnap` | — | datos de captura de matrícula usados por preview | READ | `web/js/pages/preview.js:4341-4359` |
| `GET /System/LicensePlateSurveillanceUIDesignCfg` | — | configuración de vigilancia de matrículas | READ | `web/js/pages/preview.js:4371-4390` |

### Usuarios, mantenimiento y firmware

| Método y ruta | Solicitud | Respuesta usada | Clase | Uso |
|---|---|---|---|---|
| `GET /System/Users` | — | `UserList/User`: `ID`, `UserName`, `Password`, `UserLevel` | READ (pero expone material sensible si el dispositivo lo devuelve) | `web/nvr/js/system/userManagemen.js:81-108` |
| `POST/PUT/DELETE /System/Users/1` | `<User><ID/><UserName/><Password/><IPAddress/><MACAddress/><UserLevel/><IsPasswordEncrypted/></User>`; POST crea, PUT modifica y DELETE borra; el formulario de primer acceso omite `IsPasswordEncrypted` | `statusCode` | **DANGEROUS** | `web/nvr/js/system/userManagemen.js:114-205`; `web/js/pages/login.js:45-65` |
| `GET/PUT /System/DeviceAutoMaintenance` | XML devuelto, con campos de mantenimiento/reinicio automático manipulados por la página | GET conserva `gs_xml_doc`; PUT estado HTTP | WRITE-REVERSIBLE | `web/nvr/js/system/systemManit.js:146-166,278-303` |
| `PUT /System/FactoryDefaultV2` | vacío | finalización | **DANGEROUS** | `web/nvr/js/system/systemManit.js:418-430` |
| `PUT /System/FactoryDefault` | vacío | finalización | **DANGEROUS** | `web/nvr/js/system/systemManit.js:434-446` |
| `PUT /System/Reboot` | vacío | finalización | **DANGEROUS** | `web/nvr/js/system/systemManit.js:451-467` |
| `POST /System/FirmwareUpgrade` | `FormData`: `username`, `password`, `size`, archivo bajo `file`; XHR directo | XML `ResponseStatus/statusCode` | **DANGEROUS** | `web/nvr/js/system/deviceUpgrade.js:78-116` |
| `PUT /System/CloudUpgradeServerInfo` | `<CloundUpgradeServer><Port/><HostIP/><Username/><Password/><Protocol/><Path/></CloundUpgradeServer>` obtenido del servicio cloud | estado HTTP | WRITE-REVERSIBLE | `web/nvr/js/system/deviceUpgrade.js:123-140` |
| `GET /System/CloudUpgradeFirmwareList` | — | error `ResponseStatus/statusCode` o `Firmware/{Version,Desc,Name,TotalSize}` | READ | `web/nvr/js/system/deviceUpgrade.js:148-176` |
| `PUT /System/CloudUpgradeDownloadStart` | `<Action><Passport/><FirmwareName/><AutoUpgrade>manual</AutoUpgrade><UpgradeFileSize/><MD5/></Action>` | `statusCode` | **DANGEROUS** (inicia descarga de firmware) | `web/nvr/js/system/deviceUpgrade.js:182-204` |
| `GET /System/CloudUpgradeDownloadStatus?Passport={token}` | query | `Status/{Status,TotalSize,SentSize}`; estados 0 inactivo, 1 descargando, 2 terminado, 3 fallo | READ | `web/nvr/js/system/deviceUpgrade.js:214-242` |
| `PUT /System/CloudUpgradeStartUpgrade` | `<Action><Passport/>...</Action>` | `ResponseStatus/statusCode` | **DANGEROUS** | `web/nvr/js/system/deviceUpgrade.js:256-275` |

### Gestión de cámaras IPC frontales

| Método y ruta | Solicitud | Respuesta usada | Clase | Uso |
|---|---|---|---|---|
| `GET /System/FrontConnectDeviceStatusList` | — | `DevStatusList/DevStatus/LoginStatus`; valores vistos: `other error`, `connectting`, `connect success`, `login failed`, `connect limit`, `communicate error`, `disconnected`, `network disconnect`, `ip same`, `weak password` | READ | `web/nvr/js/channelManagement/videoManagement.js:410-443` |
| `GET /System/FrontConnectDevicetList` | — | `DevConnectList/DevConnect`: `ChannelID`, `IP`, `DevChannelCount`, `PublicNetIP`, `Domain`, `IsAddDeviceByDomain`, `MAC`, `IsActiveRegister`, `DevPort`, `ProtocolType`, `Username`, `Password`, `MediaPort`, `HTTPPort`, `DevType`, `RtspConnectFlag`, `ProductType`, `RtspUrl` | READ | `web/nvr/js/channelManagement/videoManagement.js:448-495` |
| `PUT /System/FrontConnectDevicetList` | `<DevConnectList><DevConnect><DeviceName/><IP/><Username/><Password/><MAC/><DevType/><ProtocolType/><DevChannelCount/><DevPort/><ActionState/><DID/><IsActiveRegister/><PublicNetIP/><HTTPPort/><MediaPort/><IsAddDeviceByDomain/><SerialNumber/><Domain/><RtspUrl/><RtspConnectFlag/><ProductType/></DevConnect></DevConnectList>` | `ResponseStatus/statusCode` | **DANGEROUS** cuando `ActionState=delete` o por incluir credenciales; de otro modo WRITE-REVERSIBLE | alta/edición `web/nvr/js/channelManagement/videoManagement.js:567-639`; borrado `705-758`; lote `937-982` |
| `GET /System/FrontDeviceDetectList` | — | `DetectList/DevDetect`: `DeviceName`, `IP`, `MAC`, `IsActiveRegister`, `DevPort`, `ProtocolType`, `SerialNumber`, `PublicNetIP` | READ (puede disparar descubrimiento local en el dispositivo; no hay evidencia de mutación) | `web/nvr/js/channelManagement/videoManagement.js:892-929` |

`ProtocolType` se obtiene del `Type0` de `/Protocol.html`; la UI muestra ese protocolo privado con su `Name0`, añade explícitamente `6 = ONVIF`, y para alta RTSP fuerza `ProtocolType=6`, `RtspConnectFlag=true`, guardando `RtspUrl` y `Domain` (substream) codificados en Base64 (`videoManagement.js:267-281,516-566`). No se puede concluir que el valor privado sea siempre `3`, aunque el render lo etiqueta con el nombre privado cuando vale `3` (`videoManagement.js:837-840`). `ActionState` observado: `manual_add`, `auto_add`, `modify`, `delete`.

## 1. PTZ, zoom, foco, iris y presets

El preview **no usa WebSocket para PTZ** en los archivos presentes: toma el `id` HTML del botón y hace `PUT /PTZ/{ch}/{cmd}`. Los comandos exactos son:

`TurnUpLeft`, `TurnUp`, `TurnUpRight`, `TurnLeft`, `AutoLeftRight`, `TurnRight`, `TurnDownLeft`, `TurnDown`, `TurnDownRight`, `ZoomIn`, `ZoomOut`, `FocusFar`, `FocusNear`, `ApertureBigger`, `ApertureSmaller` (`web/preview.html:176-211`). En `mousedown` envía `Param1=1&Param2={speed}` y en `mouseup`, `Param1=0&Param2={speed}`; `speed` es el valor 1..10 del slider. IPC usa canal 1 y NVR `config.thoroughfare + 1` (`web/js/pages/preview.js:1804-1817,4197-4214`). Clase: WRITE-REVERSIBLE.

Presets, cero-based en el cuerpo aunque la UI muestra 1..256:

- `PUT /PTZ/{ch}/Presets/Set`, cuerpo `Param1={indice 0..255}`, respuesta `statusCode` (`preview.js:1682-1714`).
- `PUT /PTZ/{ch}/Presets/Goto`, mismo cuerpo/respuesta (`preview.js:1727-1759`).
- `PUT /PTZ/{ch}/Presets/Remove`, mismo cuerpo/respuesta, **DANGEROUS** por borrar el preset (`preview.js:1773-1794`).
- `GET/PUT /PTZ/{ch}/Config`: GET lee `PTZConfigChannel/Speed`; PUT reenvía el XML cambiando `Speed` (`preview.js:3174-3222`).
- `PUT /PTZ/1/Light`, `Param1=1|0`; `PUT /PTZ/1/RainBrush`, `Param1=1|0` (`preview.js:1878-1911,1939-1972`). El canal está fijado en 1 incluso en NVR.

No aparece un mensaje WebSocket `fun:"ptz"` en el mirror. Si el componente EasyVideo interno lo usa, su worker ausente impide demostrar formato o valores.

## 2. Audio, talk/intercom y stream de audio

La configuración persistente usa `GET/PUT /Streams/AudioStream` (catálogo anterior). La escucha y el intercom del reproductor EasyVideo se mandan como JSON al WebSocket **local** configurado en `ws://127.0.0.1:{websocketPort}/WebSocket/Connect`, mediante el worker ausente:

- Abrir/cerrar escucha: `Fountion:"live"`, `LiveCtrl.type=2`, `uniqueId`, `channel` (IPC 0; NVR el canal pasado), `Audio.mute=false|true`, `Audio.sampleRate=8000` (`web/js/components/easyvideojh.js:315-360`).
- Abrir talk: `Fountion:"live"`, `LiveCtrl.type=3`, `uniqueId`, `channel`, `Talk.open=true`, `Talk.toDevice=false` para IPC/cámara frontal o `true` para hablar al NVR local, `sampleRate=8000`, `bitPerSample=16`, `codec=1` (comentado como G.711 μ-law), `soundChannel=0`; cierre sólo manda `Talk.open=false` (`easyvideojh.js:372-428`).
- El ActiveX alternativo llama `StartVoiceStream(0)`/`StopVoiceStream(0)` y `PlayAudio(ch)`; su transporte no está visible (`web/nvr/js/iePlugin.js:142-176`).

No hay código en el mirror que capture PCM del micrófono, lo codifique o construya frames binarios. Esa lógica estaría en `js/EasyVideo/downloadereasy.js` o en el ejecutable/plugin, que no fue incluido. Por tanto no es determinable estáticamente si las muestras viajan por el mismo WebSocket, en qué framing, ni si `codec=1` se aplica antes o después del envío.

## 3. Protocolo WebSocket `/WebSocket/Connect`

Hay **dos protocolos superpuestos**:

1. Conexión directa al Boa: `ws://{hostname}:{puerto HTTP}/WebSocket/Connect`, usada sólo para eventos en el código disponible. El primer frame es JSON:

```json
{"auth":{"user":"…","mode":"0","time":"<epoch-ms>","pass":"md5(password + time)"},"req":{"fun":"event","ctrl":"open","ch":"1","stream":"…"}}
```

La construcción exacta está en `web/nvr/js/common.js:1622-1655`; el worker hace `socket.send(JSON.stringify(msg))` (`web/js/components/downloaderenvent.js:27-44,76-91`). La respuesta se trata como **texto JSON** con `cmd:"event"` y `body.type`, `body.param2` (canal visual), `body.param3` (4 = fin). Tipos observados en el manejador: `videoShade`, `leavePost`, `outOfBound` y otros eventos continuados en `common.js:1674-1880`. El único valor `req.fun` demostrable es `event`; `ctrl=open`, `ch="1"` y `stream` proviene del constructor.

2. Puente EasyVideo local: `ws://127.0.0.1:32858/WebSocket/Connect` o puerto recibido por query. Sus objetos usan la clave mal escrita `Fountion`, no `auth/req`: valores observados `createDev`, `destroy`, `live`, `playback`, `snapshot`, `record`, `download`, `settings` (`web/js/EasyVideo/playervideo.js:62-238`; `web/js/components/easyvideojh.js:15-1409`). `createDev` incluye `Auth.{usr,address,pwd}` en claro para el proceso local y `Device.{MAC,totalChannel,stream,mediaPort,cmdPort,...}`.

En particular, **no aparecen** `req.fun=live`, `playback`, `talk` o `ptz` en ningún archivo incluido. Esos nombres sólo existen como `Fountion`/subobjetos del puente, y PTZ se ejecuta por HTTP. Tampoco existe el worker `js/EasyVideo/downloadereasy.js` que abre el socket y parsea frames; el único worker presente (`downloaderenvent.js`) no fija `binaryType` para eventos y devuelve `evt.data` sin parseo binario. Por ello no se puede documentar honestamente cabecera, endianess, offsets, timestamps ni payload de frames binarios. No se debe inferir de APIs similares.

Mensajes EasyVideo relevantes además de audio:

- `Fountion:"live"`, `LiveCtrl.type=1` para preview (`Preview.open`, `streamType`, geometría, layout); tipos 5 fullscreen, 6 aspect ratio, 7 cambio de stream, 8 bitrate, 9 visibilidad, 10 movimiento de ventana y 11 split (`easyvideojh.js:15-304,513-610`).
- `Fountion:"playback"`, `PlaybackCtrl.type=1` (`Preview.play`: 1 iniciar, 2 pausar/reanudar según función, 3 parar, 4 frame), tipo 2 velocidad (`Speed.fast`), 3 audio (`Audio.mute`), 4 posición (`Position.newPos`), 5 consulta de posición, 8/9 ventana (`easyvideojh.js:642-900,943-991`).
- `Fountion:"record"` y `snapshot` controlan grabación/captura local; `download` usa `DownloadParam.operate`, `Playback.{channel,startTime,endTime,recordType,streamType}` o `Interrupt.taskId` (`easyvideojh.js:437-506,909-941`).

## 4. Búsqueda/reproducción y eventos/movimiento

La búsqueda mensual y diaria está descrita en el catálogo. Tras obtener los intervalos, la reproducción no descarga el archivo mediante HTTP del equipo: construye mensajes EasyVideo `Fountion:"playback"`. El inicio lleva `PlaybackCtrl.type=1`, `Preview.remote=true`, `play=1`, `recordType=-1`, `playPos=m_szStartTimeSet`, geometría de ventana y canal cero-based (`web/js/components/easyvideojh.js:679-715`). Pausa/reanuda, velocidad, frame, audio, seek y parada usan los tipos enumerados en la sección WebSocket. El camino ActiveX llama `StartRemotePlayback` con IP, `commandPort`, usuario, contraseña y XML de petición; el protocolo de puerto de comandos no está en el mirror (`web/js/pages/remoteplayback.js:251-293`).

Movimiento se configura por `GET/PUT /Pictures/{ch}/Motion`; pérdida de vídeo y sus enlaces (mail, beep/sirena acústica, salida de alarma y acción PTZ `preset|pattern|patrol`) usan `/Pictures/{ch}/VideoLost`. Eventos en vivo llegan por WebSocket `fun=event`. No hay endpoint HTTP específico para “borrar grabación” en la UI; el único borrado destructivo de almacenamiento es formatear.

## 5. Iluminación, IR-cut, luz blanca y sirena

- El control detallado está en `GET/PUT /Images/{ch}/IrCutFilter`. Modos literales visibles: `color`, `intelligent`, `variableinfraredlight`, `variablewhitelight`, `common`, `normal white` (`web/nvr/js/image/lighting.js:1,720-763`). Campos exactos de horarios, brillo y umbrales figuran en el catálogo y en `lighting.js:1163-1187`.
- Capacidades de la placa de luz: `GET /System/LightCap/{ch}`. El control simple del preview: `PUT /PTZ/1/Light` con `Param1=1|0`.
- No existe un endpoint de “siren” independiente. La UI sólo evidencia `BeepAlert/Enable` y `AlarmOut/Enable` como enlaces de `VideoLost`, y capacidades `AudioAlarmType`/`LightAlarm`; el sonido concreto y sus valores no son determinables sin el XML del equipo.

## 6. Gestión IPC y protocolos

Véase el catálogo `FrontConnect*`. El flujo es: descubrir con `FrontDeviceDetectList`, leer conectadas/estado, y escribir `FrontConnectDevicetList` con `ActionState`. Las credenciales de cada IPC viajan como texto XML (`Username`, `Password`). ONVIF es valor `6`; el protocolo privado se descubre dinámicamente en `/Protocol.html`. El alta manual acepta IP o dominio y puertos HTTP/command/media; el modo RTSP guarda ambas URL en Base64. No hay negociación ONVIF visible ni SOAP en el cliente: la realiza el NVR.

## 7. Hosts externos, locales y URLs referenciadas

- `http://utilerp.p6sai.com:8090/UtilERP/device/h5CloudUpgradeHost`: consulta activa del host de actualización cloud (`web/nvr/js/system/deviceUpgrade.js:45-66`).
- `http://erp.zwcloud.wang:8090/UtilERP/device/web/last-pack?...`: consulta activa de último paquete/plugin (`web/nvr/js/common.js:1118-1138`).
- `http://erp.zwcloud.wang:8090/UtilERP/device/web/download-wasm?...`: descarga activa de WASM en `web/js/components/util.js:1122-1148`; duplicada pero comentada en `login.js:535-539`.
- `http://139.9.6.140:8090/...`: alternativa/test comentada, no ejecutada (`util.js:1123`, `login.js:536`).
- `http://www.zwcloud.wang/ver10/XMLSchema`: URI de namespace XML; el navegador no demuestra que la resuelva ni la contacte.
- `http://localhost:{webServerPort}` y `ws://127.0.0.1:{websocketPort}/WebSocket/Connect`: servicios auxiliares locales para modo p6s/P2P/EasyVideo (`nav-menu.js:12,18,68`).
- RTSP mostrado al usuario: `rtsp://{device}:{RTP}/live/channel/MAIN` y `/SUB` (`web/nvr/js/network/port.js:29-30`).
- `benalman.com` y `broofa.com` son sólo atribuciones en comentarios, no peticiones (`util.js:511`, `Math.uuid.js:3`).

Las tres llamadas externas usan `dataType:"jsonp"`. El override global sólo trata especialmente los literales `json`, `text`, `html` y `script`; `jsonp` no coincide y por ello la URL absoluta queda sin el prefijo de `config.baseUrl` en el código analizado (`ajax.ext.js:14-28,66-72`). Son destinos pretendidos, no conexiones verificadas.

## 8. Credenciales, claves y posibles backdoors

No se encontró contraseña, API key, secreto cloud ni credencial Basic fija. Sí hay aspectos sensibles:

- Nombres privilegiados hardcodeados `admin` y `super_admin` usados sólo para autorización del lado cliente; esto no demuestra un bypass servidor (`web/js/pages/preview.js:1671,1805`; múltiples pantallas).
- Los formularios de IPC prellenan usuario `admin`, pero **no** contraseña (`web/nvr/js/channelManagement/videoManagement.js:58,252,699`; HTML `:162,227`).
- Cookie `userInfo = Base64(usuario:contraseña)` y parámetros P2P `user`/`password` decodificados desde la URL; ambos exponen credenciales al contexto del navegador y no constituyen cifrado (`login.js:324-330`; `nav-menu.js:15-16,76-77`).
- `localStorage.Token` comienza como la cadena `default`; el token real viene del header de autenticación. No es una llave fija (`login.js:9,258-260`).
- Auth WebSocket directo usa MD5 sin nonce del servidor: `md5(password + epoch_ms)`. Es un esquema observado, no una afirmación de seguridad criptográfica (`nvr/js/common.js:1642-1646`).
- EasyVideo entrega `Auth.pwd` en claro al servicio WebSocket local (`playervideo.js:69-89,116-135`).
- La API `GET /System/FrontConnectDevicetList` hace que la UI lea `Password` de IPCs, y `GET /System/Users` intenta leer `Password`; el contenido real (vacío, hash o claro) no puede saberse estáticamente.
- No se encontró ruta, bandera o contraseña que pueda calificarse como backdoor de depuración. Los `debugger` encontrados están comentados (`web/js/components/util.js:1212,1269`).

## Recursos/rutas que no son API del equipo

`custom/deviceType.json`, `custom/language.json`, `custom/copyright.json`, `custom/playMode.json` y páginas HTML son recursos estáticos del UI. `/mixin*` son proxies P2P genéricos, no operaciones semánticas nuevas. `/System/DeviceConfigFile?FileType=9` sólo aparece en una condición del decodificador P2P, sin ninguna llamada en el mirror (`ajax.ext.js:115`): método, cuerpo y efecto no son determinables; la condición únicamente indica que su hipotética respuesta sería Base64 decodificada como texto en vez de XML, y no se le puede asignar una clase de seguridad basada en una operación inexistente. Del mismo modo, los stubs `ipc/js/common.js` y `hvr/js/common.js` contienen “The requested URL ... was not found”; no aportan endpoints analizables.
