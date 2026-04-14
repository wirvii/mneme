Convertir un diagnóstico de bug en una issue de corrección.

1. Leé el diagnóstico en: $ARGUMENTS
2. Verificá que tiene root cause identificado con confianza
   suficiente (Confirmado o Altamente probable)
3. Si la confianza es baja, advertíme antes de continuar
4. Mostrá el resumen del diagnóstico
5. Ayudáme a redactar la issue usando el template
   .claude/templates/bug-issue-template.md
6. Cuando yo apruebe la issue, lanzá `@architect` con:
   - La issue de corrección
   - El diagnóstico original como contexto
7. Cuando el arquitecto produzca la spec, mostrámela y esperá mi aprobación
8. Spec aprobada → continuar con implementación (flujo de features Fase 2a/2b/3)

IMPORTANTE: SIEMPRE pasar por el Arquitecto. Sin excepciones.
El arquitecto define la spec técnica de corrección, incluyendo los
contratos API si aplican.
