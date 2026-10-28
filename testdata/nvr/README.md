Fixtures saneados a partir de captures/api (git-ignored).
Se sustituyeron SerialNum, ChipID, UUID/DID, SubKey, MAC y contraseñas por valores ficticios.
Comprobación: un grep sobre testdata/ con el número de serie, ChipID, DID y MAC reales (tomados de captures/) no debe devolver nada.
