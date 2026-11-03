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
