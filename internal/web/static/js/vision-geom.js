(function (root) {
  "use strict";

  function letterboxRect(width, height, mediaWidth, mediaHeight) {
    if (width <= 0 || height <= 0 || mediaWidth <= 0 || mediaHeight <= 0) {
      return { x: 0, y: 0, width: 0, height: 0 };
    }
    const scale = Math.min(width / mediaWidth, height / mediaHeight);
    const fittedWidth = mediaWidth * scale;
    const fittedHeight = mediaHeight * scale;
    return {
      x: (width - fittedWidth) / 2,
      y: (height - fittedHeight) / 2,
      width: fittedWidth,
      height: fittedHeight,
    };
  }

  function normalizePoint(point, rect) {
    if (!rect || rect.width <= 0 || rect.height <= 0) return null;
    return {
      x: Math.max(0, Math.min(1, (point.x - rect.x) / rect.width)),
      y: Math.max(0, Math.min(1, (point.y - rect.y) / rect.height)),
    };
  }

  function denormalizePoint(point, rect) {
    return {
      x: rect.x + point.x * rect.width,
      y: rect.y + point.y * rect.height,
    };
  }

  function pointInPolygon(point, polygon) {
    if (!Array.isArray(polygon) || polygon.length < 3) return false;
    let inside = false;
    for (let i = 0, j = polygon.length - 1; i < polygon.length; j = i++) {
      const a = polygon[i];
      const b = polygon[j];
      const crosses = (a.y > point.y) !== (b.y > point.y) &&
        point.x < ((b.x - a.x) * (point.y - a.y)) / (b.y - a.y) + a.x;
      if (crosses) inside = !inside;
    }
    return inside;
  }

  function activeZones(zones, pose) {
    return (zones || []).filter((zone) => {
      const preset = Number(zone?.pose?.preset || 0);
      if (preset === 0) return true;
      return !!pose && !pose.displaced && Number(pose.preset) === preset;
    });
  }

  // Radio en píxeles del bitmap para que el punto mida screenRadius px en pantalla.
  function captureRadius(screenRadius, bitmapWidth, displayWidth) {
    if (!(screenRadius > 0)) return 0;
    if (!(bitmapWidth > 0) || !(displayWidth > 0)) return screenRadius;
    return (screenRadius * bitmapWidth) / displayWidth;
  }

  // Índice del punto bajo el cursor, a lo sumo a `radius` del centro. -1 si no hay.
  function nearestPoint(points, cursor, radius) {
    if (!cursor || !(radius >= 0) || !Array.isArray(points)) return -1;
    let found = -1;
    let best = radius;
    for (let i = 0; i < points.length; i++) {
      const point = points[i];
      if (!point || !Number.isFinite(point.x) || !Number.isFinite(point.y)) continue;
      const distance = Math.hypot(point.x - cursor.x, point.y - cursor.y);
      if (distance <= best) {
        best = distance;
        found = i;
      }
    }
    return found;
  }

  // Copia los puntos y mueve `index` al cursor, en coordenadas normalizadas del rectángulo.
  function dragPoint(points, index, cursor, rect) {
    const next = (points || []).map((point) => ({ x: point.x, y: point.y }));
    if (index < 0 || index >= next.length) return next;
    const moved = normalizePoint(cursor, rect);
    if (moved) next[index] = moved;
    return next;
  }

  const api = {
    letterboxRect, normalizePoint, denormalizePoint, pointInPolygon, activeZones,
    captureRadius, nearestPoint, dragPoint,
  };
  root.VisionGeom = api;
  if (typeof module !== "undefined" && module.exports) module.exports = api;
})(typeof globalThis !== "undefined" ? globalThis : this);
