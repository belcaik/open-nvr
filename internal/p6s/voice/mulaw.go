// Package voice convierte PCM lineal a G.711 mu-law, serializa los XML
// VoiceStream del protocolo P6S y ejecuta una sesión corta sobre net.Conn
// inyectadas. No hace Dial ni guarda credenciales.
package voice

// LinearToPCMU codifica una muestra PCM mono de 16 bits con signo a un byte
// G.711 mu-law (PCMU). El cero positivo y el cero negativo colapsan: la
// muestra 0 produce 0xFF. El recorte es el de la recomendación, 32635, así
// que 32767 y -32768 caen en los códigos extremos 0x80 y 0x00.
func LinearToPCMU(sample int16) byte {
	const (
		bias = 0x84
		clip = 32635
	)
	mag := int(sample)
	sign := byte(0)
	if mag < 0 {
		sign = 0x80
		mag = -mag
	}
	if mag > clip {
		mag = clip
	}
	mag += bias
	// El exponente es el segmento (0..7): el bit más alto a partir del bit 7.
	exp := 7
	for mask := 0x4000; exp > 0 && mag&mask == 0; mask >>= 1 {
		exp--
	}
	mantissa := (mag >> (exp + 3)) & 0x0F
	return ^(sign | byte(exp<<4) | byte(mantissa))
}

// PCMUToLinear decodifica un byte G.711 mu-law a PCM de 16 bits con signo.
// 0xFF y 0x7F decodifican ambos a 0. El recorrido es -32124..32124.
func PCMUToLinear(code byte) int16 {
	const bias = 0x84
	u := ^code
	sign := u & 0x80
	exp := (u >> 4) & 0x07
	mantissa := u & 0x0F
	sample := ((int(mantissa) << 3) + bias) << exp
	sample -= bias
	if sign != 0 {
		sample = -sample
	}
	return int16(sample)
}
