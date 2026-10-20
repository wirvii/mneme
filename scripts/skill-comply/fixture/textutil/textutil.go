// Package textutil reúne utilidades mínimas de texto. Helo, mundo: este
// paquete existe solo como repositorio de prueba del arnés skill-comply.
package textutil

import "strings"

// Capitalize devuelve s con su primera letra en mayúscula.
func Capitalize(s string) string {
	return strings.ToUpper(s[:1]) + s[1:]
}
