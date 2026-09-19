# Delivery-v2: contratos de entrega y WORK

Delivery-v2 es el motor beta para ejecutar un cambio desde un contrato único y medible. El motor heredado, `legacy`, sigue siendo el predeterminado. Instalar o actualizar mneme distribuye los manuales, pero nunca activa esta beta.

## Cuatro decisiones independientes

Cada trabajo separa cuatro dimensiones que no deben confundirse:

| Dimensión | Valores | Decisión |
|---|---|---|
| Workflow | `organic` o `sdd` | Indica si el contrato nace directamente o desde una spec aprobada. |
| Lane | `trivial` o `standard` | Sólo existe para un origen SDD y limita el alcance de la spec. |
| Método de desarrollo | `standard` o `tdd` | Define si el contrato exige pruebas antes del cambio. |
| Profundidad | entrega acotada o auditoría profunda | Se elige por separado; la auditoría profunda se ejecuta explícitamente. |

Con origen SDD, la spec define y aprueba el contrato. Después, un único WORK ejecuta ese contrato. SPEC y WORK no se mantienen como ciclos paralelos para el mismo cambio. Con origen `organic`, WORK recibe el objetivo y el alcance directamente.

## Contrato y estados

El contrato reúne objetivo, alcance, criterios, restricciones y verificaciones. Al bloquearlo, mneme fija el commit base, la revisión y una huella del contenido. Una enmienda crea una revisión nueva; no reescribe la historia anterior.

El recorrido normal es `draft` → `implementing` → `reviewing` → `done`. Una revisión inicial con bloqueantes puede llevar a `correcting`; la revisión dirigida posterior termina en `done` o `escalated`. `work_resume` reanuda desde `escalated` sólo después de una decisión humana explícita.

Los hallazgos `contract_violation`, `regression` y `architecture_violation` bloquean. `discovery` e `improvement` quedan registrados, pero no amplían por sí solos el contrato ni impiden el cierre.

## Operaciones y autoridad

| Operación | Autoridad | Efecto |
|---|---|---|
| `work_begin` | Coordinador | Crea el borrador. |
| `work_get` | Cualquier rol | Lee el agregado completo sin transición. |
| `work_lock` | Coordinador | Fija base, revisión y huella antes de escribir. |
| `work_amend` | Coordinador | Crea una revisión del contrato y conserva la historia. |
| `work_review` | `qa-tester` como subagente; coordinador desde su canal | Registra una revisión ligada al commit. La restricción de subagente falla cerrada si el rol no puede resolverse. |
| `work_verify` | Implementador o coordinador | Evalúa hechos y persiste evidencia; no cambia el estado ni cierra el trabajo. |
| `work_complete` | Coordinador | Cierra sólo con evidencia verde vigente. |
| `work_resume` | Coordinador | Reanuda por decisión humana y motivo; reinicia el presupuesto sin borrar historia. |
| `work_metrics` | Cualquier rol | Lee métricas locales; nunca es una puerta de cierre. |

Ningún subagente llama `spec_advance`.

## Un ciclo acotado

1. `work_begin` crea el contrato y, si corresponde, la persona lo aprueba.
2. `work_lock` fija el contrato antes de la primera escritura.
3. El implementador trabaja dentro del alcance.
4. `work_review` realiza una revisión amplia única.
5. Si hay bloqueantes y queda presupuesto, ocurre como máximo una corrección automática.
6. Un segundo `work_review` realiza una revisión dirigida a esos bloqueantes.
7. Si persiste un bloqueante, el WORK queda `escalated`; no existe otra arista de revisión dirigida a corrección.
8. Con evidencia verde vigente, el coordinador ejecuta `work_complete`.

`work_verify` puede ejecutarse para obtener evidencia factual, pero no realiza ninguna transición. `work_metrics` sólo observa resultados posteriores.

## Activar la beta de forma explícita

La **configuración personal** de `~/.mneme/config.toml` se aplica a todo el host: alcanza a todos los repositorios que usan ese binario. No es una preferencia por repositorio. Antes de la campaña de fase 10:

1. Compila e instala el binario de la rama beta sin publicarlo.
2. Conserva una copia recuperable de `~/.mneme/config.toml`.
3. Ejecuta `mneme install claude-code`, `mneme install codex` o ambos para distribuir los manuales de esa compilación. La instalación no activa el motor ni modifica `[workflow]`.
4. En Codex, revisa y confía los hooks con `/hooks`.
5. Edita manualmente la configuración:

   ```toml
   [workflow]
   engine = "delivery_v2"
   default = "sdd"
   development_method = "standard"
   max_correction_rounds = 1
   deep_quality = "manual"
   ```

6. Usa `default = "sdd"` en este repositorio porque sus cambios parten del flujo SDD. Otros repositorios pueden elegir `organic` de forma explícita.
7. Comprueba la resolución efectiva con `mneme config show workflow`.
8. Reinicia las sesiones de agente y sólo entonces crea los WORK reales de fase 10.

No existe `mneme config set`; la activación requiere editar el archivo TOML y comprobar el resultado.

## Revertir sin perder historia

Para salir de la beta cambia sólo:

```toml
[workflow]
engine = "legacy"
```

Después ejecuta `mneme config show workflow` y reinicia las sesiones. La reversión impide nuevas mutaciones, pero mantiene disponibles `work_get` y `work_metrics`. No elimina contratos, hallazgos, certificados, historia ni archivos `.mneme/sdd/work/WORK-###.md`, y no cambia el flujo SDD heredado.

## Entrega acotada y auditoría profunda

La entrega acotada usa `work_review`, `work_verify` y `work_complete` para evaluar el contrato. La auditoría profunda sigue viviendo en `mneme quality verify`, con cobertura, criterios profundos, presupuesto contra el grafo, mutación y comprobación visual declarada.

`workflow.deep_quality` acepta `manual` y `always`, pero en esta beta `deep_quality = "always"` no ejecuta por sí solo `quality verify`. La campaña usa `manual`; cuando haga falta una auditoría profunda, el coordinador la ejecuta de forma explícita y registra que es una medición separada.

## Transporte git-native y evidencia local

El motor y el transporte se activan por separado. `engine = "delivery_v2"` no crea el marcador `.mneme/sdd/.mneme-sdd`, y `mneme sdd enable` no activa el motor.

Cuando el transporte está habilitado, `.mneme/sdd/work/WORK-###.md` lleva contrato, criterios, restricciones, hallazgos e historia. Los certificados y checks permanecen locales. Por eso un WORK importado puede mostrar evidencia `complete`, `partial` o `not_started`; `work_metrics` nunca interpreta la ausencia local como un cero real.

## Límite de esta fase

Las métricas son locales y descriptivas. La campaña de fase 10 —cinco cambios pequeños, cinco medianos y dos sustanciales— permanece fuera de esta fase. Este documento no aporta comparaciones ni atribuye causalidad: primero debe existir evidencia real de esa campaña.
