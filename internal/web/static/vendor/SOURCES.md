# Archivos de terceros

| Archivo | Origen | Versión | sha256 |
|---|---|---|---|
| htmx.min.js | https://unpkg.com/htmx.org@2.0.4/dist/htmx.min.js (coincide con jsDelivr) | 2.0.4 | e209dda5c8235479f3166defc7750e1dbcd5a5c1808b7792fc2e6733768fb447 |
| video-rtc.js | https://raw.githubusercontent.com/AlexxIT/go2rtc/v1.9.15/www/video-rtc.js (MIT, ver LICENSE-go2rtc) | 1.9.15 (debe coincidir con la imagen go2rtc del compose) | d48ce627baf7c341a92c0f5844a3c546431f9db873ff21489671aba2ecfe64fb |

`video-stream.js` de go2rtc no se usa: inserta un `<style>` en línea que la
CSP bloquea. `js/video.js` extiende directamente `VideoRTC`.
