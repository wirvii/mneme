# Spec: {ISSUE-ID} — {Titulo descriptivo}

## Contexto

{Por que se necesita este cambio? Que problema resuelve? Que pidio el usuario?}

## Objetivo

{Que debe lograr la implementacion. Una oracion clara.}

## Scope

### Apps/Modulos afectados

| App/Modulo | Tipo de cambio |
|------------|---------------|
| `apps/xxx` | {nuevo/modificacion/eliminacion} |

### Fuera de scope

- {Que explicitamente NO se va a hacer en esta spec}

## Diseno Tecnico

### Backend

{Describir cambios: entidades, casos de uso, queries, migraciones, endpoints}

#### Endpoints

| Metodo | Ruta | Request | Response |
|--------|------|---------|----------|
| `POST` | `/v1/resource` | `{ field: type }` | `{ data: {...} }` |

#### Migraciones

```sql
-- UP
{DDL propuesto}

-- DOWN
{DDL de rollback}
```

### Frontend

{Describir cambios: paginas, componentes, actions, fetchers, traducciones}

#### Traducciones requeridas

| Key | Valor |
|-----|-------|
| `module.section.key` | "Texto visible" |

## Criterios de Aceptacion

1. {Criterio concreto y verificable}
2. {Maximo 6 criterios. Si son mas, partir en sub-issues.}

## Consideraciones

- **Permisos**: {Que permisos se requieren}
- **Seguridad**: {Consideraciones de seguridad}
- **Performance**: {Consideraciones de rendimiento}
- **Migracion de datos**: {Si aplica}

## Dependencias

- {Specs o implementaciones que deben completarse antes}
- {Servicios externos que se requieren}
