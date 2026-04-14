# QA Checklist — TypeScript Frontend

## Analisis Estatico

Correr SOLO sobre archivos `.ts`/`.tsx` modificados.
Identificar archivos afectados con: `git diff --name-only | grep -E '\.(ts|tsx)$'`

### Checks de compilacion

| Check | Que detecta | Severidad |
|-------|-------------|-----------|
| `pnpm typecheck` | Errores de tipos — el equivalente a `go vet` | CRITICO |
| `pnpm build` | Errores de compilacion que typecheck no cubre (imports, SSR, rutas) | CRITICO |
| `eslint` sobre archivos modificados | Reglas del proyecto violadas | IMPORTANTE |

### Busqueda de anti-patrones en archivos modificados

Para cada archivo `.ts`/`.tsx` modificado, buscar estos patrones con grep:

| Patron a buscar | Que detecta | Severidad |
|-----------------|-------------|-----------|
| `": any"` o `as any` | Agujeros en el type system — bugs que TypeScript no puede detectar | IMPORTANTE |
| `console.log` / `console.error` | Debug que llega a produccion | IMPORTANTE |
| `fetch(` en archivos con `'use client'` | Bypass de Server Actions — patron prohibido | CRITICO |
| `useEffect` con fetch/request dentro | Data fetching en cliente — debe ser Server Component | IMPORTANTE |
| `react-hook-form` en imports | Libreria prohibida — debe ser Conform + Zod | CRITICO |
| `import` de modulo con `"server-only"` en archivo `'use client'` | Crash en runtime — servidor leakeado al cliente | CRITICO |
| Strings literales > 3 caracteres en JSX fuera de `t()` o `useTranslations` | i18n roto — texto sin traducir | IMPORTANTE |

**Reglas:**
- Si `typecheck`, `build`, `fetch en cliente`, `react-hook-form` o `server-only leak` fallan, es CRITICO — rechazar
- Si `any types`, `console.log`, `useEffect fetch`, `eslint` o `texto sin i18n` aparecen, es IMPORTANTE — rechazar
- Excluir `node_modules/`, `*.test.ts`, `*.spec.ts`, archivos generados (`gen/`)

## Review de Codigo Frontend

### Server Components
- [ ] Server Components por defecto
- [ ] `'use client'` solo donde es estrictamente necesario (interactividad real)
- [ ] No hay `useEffect` para data fetching
- [ ] No hay `useState` para datos que podrian ser server-side

### Formularios
- [ ] Se usa `conform` + `zod`, NO `react-hook-form`
- [ ] Los schemas estan en `schema.ts`
- [ ] Los Server Actions estan en `actions.ts`
- [ ] Errores de validacion se muestran al usuario

### Internacionalizacion
- [ ] Los textos vienen del sistema de i18n del proyecto
- [ ] No hay texto hardcodeado en componentes
- [ ] Las keys de traduccion son descriptivas

### Estilos y UI
- [ ] Se usan tokens semanticos, NO colores hardcodeados
- [ ] Los componentes usan la libreria del proyecto (shadcn/ui u otra)
- [ ] Dark mode considerado
- [ ] No hay estilos inline innecesarios

### Comunicacion Backend
- [ ] API via Server Actions, NO fetch directo en componentes
- [ ] Fetchers con `import "server-only"` si aplica
- [ ] Los errores de backend se mapean a mensajes de usuario

### Seguridad Frontend
- [ ] No hay XSS (inputs sanitizados)
- [ ] No hay datos sensibles en client components
- [ ] Las server actions validan permisos

### Contratos API (si existe `api-contracts.md`)
- [ ] Los tipos TypeScript del frontend coinciden con los JSON tags del backend
- [ ] La estructura de response (array plano vs paginado vs objeto) esta correctamente tipada
- [ ] Los nombres de campos en TS coinciden con los del contrato

## Performance

- [ ] No hay N+1 queries o fetch waterfalls
- [ ] No hay re-renders innecesarios (client components que podrian ser server)
- [ ] No hay imports pesados en client components que podrian lazy-loaderse
- [ ] Imagenes usan next/image cuando aplica
