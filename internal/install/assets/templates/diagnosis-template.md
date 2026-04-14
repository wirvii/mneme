# Diagnostico: {BUG-ID}

## Resumen

| Campo | Valor |
|-------|-------|
| **Bug** | {BUG-ID} |
| **Severidad** | {Critico / Alto / Medio / Bajo} |
| **Tipo** | {Funcional / Performance / Seguridad / UX / Datos / Integracion} |
| **Alcance** | {Un usuario / Grupo / Todos} |
| **Confianza** | {Confirmado / Altamente probable / Probable / Hipotesis} |
| **Complejidad del fix** | {Simple / Media / Alta} |

## Triage

### Duplicados

{Resultado de la busqueda de duplicados. "Sin duplicados" o referencia al bug anterior.}

### Clasificacion

{Justificacion de la severidad asignada.}

## Investigacion

### Flujo afectado

```
{Mapa del flujo de punta a punta con archivos y lineas}
entrada -> procesamiento -> salida
archivo:linea -> archivo:linea -> archivo:linea
```

### Cambios recientes relevantes

| Commit | Fecha | Descripcion | Relevancia |
|--------|-------|-------------|------------|
| `abc123` | YYYY-MM-DD | {descripcion} | {por que es relevante} |

### Puntos de falla analizados

| Punto | Estado | Detalle |
|-------|--------|---------|
| Manejo de errores | {OK / Problema} | {detalle} |
| Validaciones | {OK / Problema} | {detalle} |
| Edge cases | {OK / Problema} | {detalle} |
| Race conditions | {OK / Problema} | {detalle} |
| Integraciones | {OK / Problema} | {detalle} |

## Root Cause

### Hipotesis principal

{Descripcion clara del root cause con evidencia.}

- **Archivo**: `path/to/file:linea`
- **Evidencia**: {que en el codigo demuestra que este es el problema}

### Hipotesis alternativas

{Si hay mas de una posibilidad, listarlas con evidencia a favor/en contra.}

## Impacto

- **Alcance real**: {solo este caso o es sistemico}
- **Otros flujos afectados**: {listar si el patron problematico existe en otros flujos}
- **Riesgo si no se corrige**: {que pasa si se deja asi}

## Solucion propuesta

- **Estrategia**: {descripcion alto nivel de la correccion}
- **Archivos a modificar**: {lista de archivos}
- **Riesgos de la correccion**: {que podria salir mal con el fix}
- **Complejidad**: Simple / Media / Alta

## Recomendacion

{Recomendacion final clara: corregir, investigar mas, o cerrar.}
