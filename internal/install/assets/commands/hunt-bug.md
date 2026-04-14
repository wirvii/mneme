Iniciar el flujo de investigación de un bug.

1. Leé el bug report en: $ARGUMENTS
2. Verificá que el reporte tenga información mínima:
   - Descripción del problema
   - Al menos un dato concreto (ID, fecha, empresa)
   - Comportamiento esperado vs actual
3. Si falta info, decíme qué falta ANTES de lanzar al Bug Hunter
4. Si el reporte está completo:
   a. Creá la carpeta .claude/bugs/{bug-id}/ si no existe
   b. Lanzá al subagente @bug-hunter con el reporte
   c. Cuando termine, mostrá el diagnóstico y esperá mi aprobación
