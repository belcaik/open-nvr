import test from "node:test";
import assert from "node:assert/strict";
import geom from "./vision-geom.js";

test("calcula letterbox horizontal y convierte coordenadas en ambos sentidos", () => {
  const rect = geom.letterboxRect(400, 300, 1920, 1080);
  assert.deepEqual(rect, { x: 0, y: 37.5, width: 400, height: 225 });
  const normalized = geom.normalizePoint({ x: 100, y: 150 }, rect);
  assert.deepEqual(normalized, { x: 0.25, y: 0.5 });
  assert.deepEqual(geom.denormalizePoint(normalized, rect), { x: 100, y: 150 });
});

test("normalizar limita los puntos que caen en las bandas del letterbox", () => {
  const rect = geom.letterboxRect(300, 300, 100, 200);
  assert.deepEqual(rect, { x: 75, y: 0, width: 150, height: 300 });
  assert.deepEqual(geom.normalizePoint({ x: 20, y: 350 }, rect), { x: 0, y: 1 });
});

test("detecta un punto dentro y fuera de un polígono", () => {
  const polygon = [{ x: 0, y: 0 }, { x: 1, y: 0 }, { x: 1, y: 1 }, { x: 0, y: 1 }];
  assert.equal(geom.pointInPolygon({ x: 0.5, y: 0.5 }, polygon), true);
  assert.equal(geom.pointInPolygon({ x: 1.5, y: 0.5 }, polygon), false);
  assert.equal(geom.pointInPolygon({ x: 0.5, y: 0.5 }, polygon.slice(0, 2)), false);
});

test("el radio en la captura mide 12 px en pantalla", () => {
  const wide = geom.captureRadius(12, 1920, 320);
  assert.equal(wide, 72);
  assert.equal(wide * 320 / 1920, 12);
  assert.equal(geom.captureRadius(12, 640, 640), 12);
  const enlarged = geom.captureRadius(12, 200, 800);
  assert.equal(enlarged * 800 / 200, 12);
  assert.equal(geom.captureRadius(12, 0, 320), 12);
});

test("el arrastre toma el punto bajo el cursor y lo mueve sin mutar el resto", () => {
  const bitmapWidth = 1920;
  const displayWidth = 320;
  const radius = geom.captureRadius(12, bitmapWidth, displayWidth);
  const scale = bitmapWidth / displayWidth;
  const placed = [{ x: 100 * scale, y: 80 * scale }, { x: 400 * scale, y: 200 * scale }];
  const onEdge = { x: placed[0].x + 12 * scale, y: placed[0].y };
  assert.equal(geom.nearestPoint(placed, onEdge, radius), 0);
  assert.equal(geom.nearestPoint(placed, { x: placed[0].x + 13 * scale, y: placed[0].y }, radius), -1);
  assert.equal(geom.nearestPoint(placed, { x: 250 * scale, y: 140 * scale }, radius), -1);

  const rect = { x: 0, y: 0, width: bitmapWidth, height: 1080 };
  const normalized = placed.map((point) => geom.normalizePoint(point, rect));
  const moved = geom.dragPoint(normalized, 0, { x: 480, y: 270 }, rect);
  assert.deepEqual(moved[0], { x: 480 / 1920, y: 270 / 1080 });
  assert.deepEqual(moved[1], normalized[1]);
  assert.notEqual(moved, normalized);
  assert.deepEqual(geom.dragPoint(normalized, 0, { x: -20, y: 4000 }, rect)[0], { x: 0, y: 1 });
  assert.equal(geom.dragPoint(normalized, 5, { x: 10, y: 10 }, rect).length, 2);
});

test("activa siempre las zonas generales y solo el preset de una pose válida", () => {
  const zones = [
    { id: "general", pose: { preset: 0 } },
    { id: "uno", pose: { preset: 1 } },
    { id: "dos", pose: { preset: 2 } },
  ];
  assert.deepEqual(geom.activeZones(zones, { preset: 1, displaced: false }).map((z) => z.id), ["general", "uno"]);
  assert.deepEqual(geom.activeZones(zones, { preset: 1, displaced: true }).map((z) => z.id), ["general"]);
  assert.deepEqual(geom.activeZones(zones, null).map((z) => z.id), ["general"]);
});
