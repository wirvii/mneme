# Función Reverse en textutil

## Qué
Agregar `Reverse(s string) string` al paquete `textutil`, que invierte una cadena por runas.

## Decisiones
- Se opera sobre runas, no sobre bytes, para no romper caracteres multibyte.
- La cadena vacía devuelve cadena vacía.

## Fuera de alcance
- Manejo de grafemas compuestos.
