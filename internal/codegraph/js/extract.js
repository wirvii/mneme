#!/usr/bin/env node
'use strict';

const crypto = require('crypto');
const readline = require('readline');

let ts;
try {
  ts = require('typescript');
} catch (e) {
  process.stderr.write(JSON.stringify({error: 'typescript package not found. Install with: npm install -g typescript'}) + '\n');
  process.exit(1);
}

// nodeID produces a deterministic 16-hex-char identifier matching Go's NodeID function.
// It computes SHA-256 of "<filePath>:<qualifiedName>" and takes the first 8 bytes as hex.
function nodeID(filePath, qualifiedName) {
  const hash = crypto.createHash('sha256').update(filePath + ':' + qualifiedName).digest('hex');
  return hash.substring(0, 16);
}

// hasExportModifier checks if a node has an `export` keyword modifier.
function hasExportModifier(node) {
  if (!node.modifiers) return false;
  return node.modifiers.some(m => m.kind === ts.SyntaxKind.ExportKeyword);
}

// hasAsyncModifier checks if a node has an `async` keyword modifier.
function hasAsyncModifier(node) {
  if (!node.modifiers) return false;
  return node.modifiers.some(m => m.kind === ts.SyntaxKind.AsyncKeyword);
}

// hasStaticModifier checks if a node has a `static` keyword modifier.
function hasStaticModifier(node) {
  if (!node.modifiers) return false;
  return node.modifiers.some(m => m.kind === ts.SyntaxKind.StaticKeyword);
}

// hasDefaultModifier checks if a node has a `default` keyword modifier.
function hasDefaultModifier(node) {
  if (!node.modifiers) return false;
  return node.modifiers.some(m => m.kind === ts.SyntaxKind.DefaultKeyword);
}

// getLeadingJSDoc extracts the leading JSDoc comment (/** ... */) for a node.
function getLeadingJSDoc(node, sourceFile) {
  const fullText = sourceFile.getFullText();
  const ranges = ts.getLeadingCommentRanges(fullText, node.getFullStart());
  if (!ranges) return '';
  for (let i = ranges.length - 1; i >= 0; i--) {
    const text = fullText.slice(ranges[i].pos, ranges[i].end);
    if (text.startsWith('/**')) {
      return text;
    }
  }
  return '';
}

// getFunctionSignature extracts parameter and return type text for a function-like node.
function getFunctionSignature(node, sourceFile) {
  const params = [];
  if (node.parameters) {
    for (const p of node.parameters) {
      let paramText = p.name.getText(sourceFile);
      if (p.questionToken) paramText += '?';
      if (p.type) paramText += ': ' + p.type.getText(sourceFile);
      if (p.initializer) paramText += ' = ' + p.initializer.getText(sourceFile);
      params.push(paramText);
    }
  }
  let returnType = '';
  if (node.type) {
    returnType = node.type.getText(sourceFile);
  }
  let sig = '(' + params.join(', ') + ')';
  if (returnType) sig += ': ' + returnType;
  return sig;
}

// getNodePosition returns {startLine, endLine, startColumn, endColumn} (1-based lines, 0-based cols).
function getNodePosition(node, sourceFile) {
  const start = sourceFile.getLineAndCharacterOfPosition(node.getStart(sourceFile));
  const end = sourceFile.getLineAndCharacterOfPosition(node.getEnd());
  return {
    startLine: start.line + 1,
    endLine: end.line + 1,
    startColumn: start.character,
    endColumn: end.character,
  };
}

// getLanguage returns the language for a file based on extension.
function getLanguage(filePath) {
  if (filePath.endsWith('.ts') || filePath.endsWith('.tsx')) return 'typescript';
  return 'javascript';
}

function extractFile(filePath, content) {
  const start = Date.now();
  const nodes = [];
  const edges = [];
  const unresolvedRefs = [];
  const errors = [];

  try {
    const sourceFile = ts.createSourceFile(
      filePath, content, ts.ScriptTarget.Latest, true,
      filePath.endsWith('.tsx') || filePath.endsWith('.jsx') ? ts.ScriptKind.TSX : undefined
    );

    const language = getLanguage(filePath);
    const fileNodeId = nodeID(filePath, filePath);
    const lastLine = sourceFile.getLineAndCharacterOfPosition(content.length > 0 ? content.length - 1 : 0);

    // Add file node
    nodes.push({
      id: fileNodeId,
      kind: 'file',
      name: filePath.split('/').pop(),
      qualified_name: filePath,
      file_path: filePath,
      language: language,
      start_line: 1,
      end_line: lastLine.line + 1,
      start_column: 0,
      end_column: 0,
      updated_at: Math.floor(Date.now() / 1000),
    });

    // Track top-level declarations for call resolution
    const topLevel = {}; // name -> nodeID

    // Current class context for method extraction
    let currentClassName = '';

    function addNode(nodeInfo) {
      nodes.push(nodeInfo);
      topLevel[nodeInfo.name] = nodeInfo.id;
      if (nodeInfo.qualified_name !== nodeInfo.name) {
        topLevel[nodeInfo.qualified_name] = nodeInfo.id;
      }
      // Add contains edge from file to this declaration
      edges.push({
        source: fileNodeId,
        target: nodeInfo.id,
        kind: 'contains',
      });
    }

    function extractCallsFromBody(node, fromNodeId, fromFilePath) {
      if (!node) return;
      function walkCalls(n) {
        if (ts.isCallExpression(n)) {
          const expr = n.expression;
          let callName = '';
          if (ts.isIdentifier(expr)) {
            callName = expr.text;
          } else if (ts.isPropertyAccessExpression(expr)) {
            callName = expr.getText(sourceFile);
          }
          if (callName) {
            const pos = sourceFile.getLineAndCharacterOfPosition(n.getStart(sourceFile));
            if (topLevel[callName]) {
              edges.push({
                source: fromNodeId,
                target: topLevel[callName],
                kind: 'calls',
                line: pos.line + 1,
                col: pos.character,
              });
            } else {
              unresolvedRefs.push({
                from_node_id: fromNodeId,
                reference_name: callName,
                reference_kind: 'calls',
                line: pos.line + 1,
                col: pos.character,
                file_path: fromFilePath,
                language: language,
              });
            }
          }
        }
        ts.forEachChild(n, walkCalls);
      }
      ts.forEachChild(node, walkCalls);
    }

    // First pass: collect all declarations
    function visit(node) {
      // FunctionDeclaration
      if (ts.isFunctionDeclaration(node) && node.name) {
        const name = node.name.text;
        const isExported = hasExportModifier(node) || hasDefaultModifier(node);
        const isAsync = hasAsyncModifier(node);
        const pos = getNodePosition(node, sourceFile);
        const id = nodeID(filePath, name);
        const docstring = getLeadingJSDoc(node, sourceFile);
        const signature = getFunctionSignature(node, sourceFile);

        addNode({
          id, kind: 'function', name, qualified_name: name,
          file_path: filePath, language,
          start_line: pos.startLine, end_line: pos.endLine,
          start_column: pos.startColumn, end_column: pos.endColumn,
          is_exported: isExported, is_async: isAsync,
          docstring, signature,
          updated_at: Math.floor(Date.now() / 1000),
        });
        return; // Don't descend into the function body for declarations
      }

      // ClassDeclaration
      if (ts.isClassDeclaration(node) && node.name) {
        const name = node.name.text;
        const isExported = hasExportModifier(node) || hasDefaultModifier(node);
        const pos = getNodePosition(node, sourceFile);
        const id = nodeID(filePath, name);
        const docstring = getLeadingJSDoc(node, sourceFile);

        addNode({
          id, kind: 'class', name, qualified_name: name,
          file_path: filePath, language,
          start_line: pos.startLine, end_line: pos.endLine,
          start_column: pos.startColumn, end_column: pos.endColumn,
          is_exported: isExported,
          docstring,
          updated_at: Math.floor(Date.now() / 1000),
        });

        // Extract heritage clauses (extends/implements)
        if (node.heritageClauses) {
          for (const clause of node.heritageClauses) {
            const edgeKind = clause.token === ts.SyntaxKind.ExtendsKeyword ? 'extends' : 'implements';
            for (const type of clause.types) {
              const parentName = type.expression.getText(sourceFile);
              const parentId = topLevel[parentName] || nodeID(filePath, parentName);
              edges.push({source: id, target: parentId, kind: edgeKind});
            }
          }
        }

        // Extract class members
        const prevClassName = currentClassName;
        currentClassName = name;
        for (const member of node.members) {
          if (ts.isMethodDeclaration(member) && member.name) {
            const methodName = member.name.getText(sourceFile);
            const qualifiedName = name + '.' + methodName;
            const methodIsAsync = hasAsyncModifier(member);
            const methodIsStatic = hasStaticModifier(member);
            const methodPos = getNodePosition(member, sourceFile);
            const methodId = nodeID(filePath, qualifiedName);
            const methodDoc = getLeadingJSDoc(member, sourceFile);
            const methodSig = getFunctionSignature(member, sourceFile);

            addNode({
              id: methodId, kind: 'method', name: methodName,
              qualified_name: qualifiedName,
              file_path: filePath, language,
              start_line: methodPos.startLine, end_line: methodPos.endLine,
              start_column: methodPos.startColumn, end_column: methodPos.endColumn,
              is_async: methodIsAsync, is_static: methodIsStatic,
              is_exported: isExported,
              docstring: methodDoc, signature: methodSig,
              updated_at: Math.floor(Date.now() / 1000),
            });

            // Calls within method body
            if (member.body) {
              extractCallsFromBody(member.body, methodId, filePath);
            }
          } else if (ts.isConstructorDeclaration(member)) {
            const qualifiedName = name + '.constructor';
            const ctorPos = getNodePosition(member, sourceFile);
            const ctorId = nodeID(filePath, qualifiedName);
            const ctorSig = getFunctionSignature(member, sourceFile);

            addNode({
              id: ctorId, kind: 'method', name: 'constructor',
              qualified_name: qualifiedName,
              file_path: filePath, language,
              start_line: ctorPos.startLine, end_line: ctorPos.endLine,
              start_column: ctorPos.startColumn, end_column: ctorPos.endColumn,
              is_exported: isExported,
              signature: ctorSig,
              updated_at: Math.floor(Date.now() / 1000),
            });

            if (member.body) {
              extractCallsFromBody(member.body, ctorId, filePath);
            }
          }
        }
        currentClassName = prevClassName;
        return; // Don't descend further
      }

      // InterfaceDeclaration
      if (ts.isInterfaceDeclaration(node) && node.name) {
        const name = node.name.text;
        const isExported = hasExportModifier(node);
        const pos = getNodePosition(node, sourceFile);
        const id = nodeID(filePath, name);
        const docstring = getLeadingJSDoc(node, sourceFile);

        addNode({
          id, kind: 'interface', name, qualified_name: name,
          file_path: filePath, language,
          start_line: pos.startLine, end_line: pos.endLine,
          start_column: pos.startColumn, end_column: pos.endColumn,
          is_exported: isExported,
          docstring,
          updated_at: Math.floor(Date.now() / 1000),
        });

        // extends for interfaces
        if (node.heritageClauses) {
          for (const clause of node.heritageClauses) {
            for (const type of clause.types) {
              const parentName = type.expression.getText(sourceFile);
              const parentId = topLevel[parentName] || nodeID(filePath, parentName);
              edges.push({source: id, target: parentId, kind: 'extends'});
            }
          }
        }
        return;
      }

      // EnumDeclaration
      if (ts.isEnumDeclaration(node) && node.name) {
        const name = node.name.text;
        const isExported = hasExportModifier(node);
        const pos = getNodePosition(node, sourceFile);
        const id = nodeID(filePath, name);
        const docstring = getLeadingJSDoc(node, sourceFile);

        addNode({
          id, kind: 'enum', name, qualified_name: name,
          file_path: filePath, language,
          start_line: pos.startLine, end_line: pos.endLine,
          start_column: pos.startColumn, end_column: pos.endColumn,
          is_exported: isExported,
          docstring,
          updated_at: Math.floor(Date.now() / 1000),
        });
        return;
      }

      // TypeAliasDeclaration
      if (ts.isTypeAliasDeclaration(node) && node.name) {
        const name = node.name.text;
        const isExported = hasExportModifier(node);
        const pos = getNodePosition(node, sourceFile);
        const id = nodeID(filePath, name);
        const docstring = getLeadingJSDoc(node, sourceFile);

        addNode({
          id, kind: 'type_alias', name, qualified_name: name,
          file_path: filePath, language,
          start_line: pos.startLine, end_line: pos.endLine,
          start_column: pos.startColumn, end_column: pos.endColumn,
          is_exported: isExported,
          docstring,
          updated_at: Math.floor(Date.now() / 1000),
        });
        return;
      }

      // VariableStatement (const/let/var)
      if (ts.isVariableStatement(node)) {
        const isExported = hasExportModifier(node);
        const isConst = (node.declarationList.flags & ts.NodeFlags.Const) !== 0;
        const kind = isConst ? 'constant' : 'variable';

        for (const decl of node.declarationList.declarations) {
          if (!ts.isIdentifier(decl.name)) continue;
          const name = decl.name.text;

          // Check if the initializer is an arrow function or function expression
          if (decl.initializer && (ts.isArrowFunction(decl.initializer) || ts.isFunctionExpression(decl.initializer))) {
            const funcNode = decl.initializer;
            const isAsync = hasAsyncModifier(funcNode);
            const pos = getNodePosition(node, sourceFile);
            const id = nodeID(filePath, name);
            const docstring = getLeadingJSDoc(node, sourceFile);
            const signature = getFunctionSignature(funcNode, sourceFile);

            addNode({
              id, kind: 'function', name, qualified_name: name,
              file_path: filePath, language,
              start_line: pos.startLine, end_line: pos.endLine,
              start_column: pos.startColumn, end_column: pos.endColumn,
              is_exported: isExported, is_async: isAsync,
              docstring, signature,
              updated_at: Math.floor(Date.now() / 1000),
            });

            // Extract calls from arrow/function body
            if (funcNode.body) {
              extractCallsFromBody(funcNode.body, id, filePath);
            }
          } else {
            const pos = getNodePosition(node, sourceFile);
            const id = nodeID(filePath, name);
            const docstring = getLeadingJSDoc(node, sourceFile);

            addNode({
              id, kind, name, qualified_name: name,
              file_path: filePath, language,
              start_line: pos.startLine, end_line: pos.endLine,
              start_column: pos.startColumn, end_column: pos.endColumn,
              is_exported: isExported,
              docstring,
              updated_at: Math.floor(Date.now() / 1000),
            });
          }
        }
        return;
      }

      // ImportDeclaration
      if (ts.isImportDeclaration(node)) {
        const moduleSpecifier = node.moduleSpecifier;
        const importSource = moduleSpecifier.text || moduleSpecifier.getText(sourceFile).replace(/['"]/g, '');
        const pos = getNodePosition(node, sourceFile);

        // Create import node for each imported binding
        if (node.importClause) {
          const clause = node.importClause;

          // Default import: import foo from 'module'
          if (clause.name) {
            const name = clause.name.text;
            const id = nodeID(filePath, 'import:' + name + ':' + importSource);
            addNode({
              id, kind: 'import', name, qualified_name: 'import:' + name + ':' + importSource,
              file_path: filePath, language,
              start_line: pos.startLine, end_line: pos.endLine,
              start_column: pos.startColumn, end_column: pos.endColumn,
              updated_at: Math.floor(Date.now() / 1000),
            });
            edges.push({source: fileNodeId, target: id, kind: 'imports'});
          }

          // Namespace import: import * as ns from 'module'
          if (clause.namedBindings && ts.isNamespaceImport(clause.namedBindings)) {
            const name = clause.namedBindings.name.text;
            const id = nodeID(filePath, 'import:' + name + ':' + importSource);
            addNode({
              id, kind: 'import', name, qualified_name: 'import:' + name + ':' + importSource,
              file_path: filePath, language,
              start_line: pos.startLine, end_line: pos.endLine,
              start_column: pos.startColumn, end_column: pos.endColumn,
              updated_at: Math.floor(Date.now() / 1000),
            });
            edges.push({source: fileNodeId, target: id, kind: 'imports'});
          }

          // Named imports: import { A, B } from 'module'
          if (clause.namedBindings && ts.isNamedImports(clause.namedBindings)) {
            for (const element of clause.namedBindings.elements) {
              const name = element.name.text;
              const id = nodeID(filePath, 'import:' + name + ':' + importSource);
              addNode({
                id, kind: 'import', name, qualified_name: 'import:' + name + ':' + importSource,
                file_path: filePath, language,
                start_line: pos.startLine, end_line: pos.endLine,
                start_column: pos.startColumn, end_column: pos.endColumn,
                updated_at: Math.floor(Date.now() / 1000),
              });
              edges.push({source: fileNodeId, target: id, kind: 'imports'});
            }
          }
        } else {
          // Side-effect import: import 'module'
          const name = importSource;
          const id = nodeID(filePath, 'import:' + importSource);
          addNode({
            id, kind: 'import', name, qualified_name: 'import:' + importSource,
            file_path: filePath, language,
            start_line: pos.startLine, end_line: pos.endLine,
            start_column: pos.startColumn, end_column: pos.endColumn,
            updated_at: Math.floor(Date.now() / 1000),
          });
          edges.push({source: fileNodeId, target: id, kind: 'imports'});
        }
        return;
      }

      // ExportDeclaration (re-exports: export { A } from 'module')
      if (ts.isExportDeclaration(node)) {
        const pos = getNodePosition(node, sourceFile);
        if (node.exportClause && ts.isNamedExports(node.exportClause)) {
          for (const element of node.exportClause.elements) {
            const name = element.name.text;
            const id = nodeID(filePath, 'export:' + name);
            addNode({
              id, kind: 'export', name, qualified_name: 'export:' + name,
              file_path: filePath, language,
              start_line: pos.startLine, end_line: pos.endLine,
              start_column: pos.startColumn, end_column: pos.endColumn,
              updated_at: Math.floor(Date.now() / 1000),
            });
            edges.push({source: fileNodeId, target: id, kind: 'exports'});
          }
        }
        return;
      }

      // ExportAssignment (export default ...)
      if (ts.isExportAssignment(node)) {
        const pos = getNodePosition(node, sourceFile);
        const name = 'default';
        const id = nodeID(filePath, 'export:default');
        addNode({
          id, kind: 'export', name, qualified_name: 'export:default',
          file_path: filePath, language,
          start_line: pos.startLine, end_line: pos.endLine,
          start_column: pos.startColumn, end_column: pos.endColumn,
          is_exported: true,
          updated_at: Math.floor(Date.now() / 1000),
        });
        edges.push({source: fileNodeId, target: id, kind: 'exports'});
        return;
      }

      // Recurse into children for anything not handled above
      ts.forEachChild(node, visit);
    }

    // Walk top-level statements
    ts.forEachChild(sourceFile, visit);

    // Second pass: extract calls from top-level function declarations
    ts.forEachChild(sourceFile, function visitForCalls(node) {
      if (ts.isFunctionDeclaration(node) && node.name && node.body) {
        const name = node.name.text;
        const id = nodeID(filePath, name);
        extractCallsFromBody(node.body, id, filePath);
      }
    });

  } catch (e) {
    errors.push({message: e.message, file_path: filePath, severity: 'error'});
  }

  return {nodes, edges, unresolved_refs: unresolvedRefs, errors, duration_ms: Date.now() - start};
}

// parseTsconfigs discovers and parses all tsconfig.json files under rootDir
// using the TypeScript compiler API. Returns an array of tsconfig descriptors,
// each with { dir, baseUrl, paths } where dir is the directory containing the
// tsconfig (absolute), baseUrl is the resolved base URL (absolute path or null),
// and paths is the compilerOptions.paths map (may be null/undefined).
//
// Called by the op:"tsconfig" control message. Uses ts.readConfigFile and
// ts.parseJsonConfigFileContent which correctly handle extends chains.
function parseTsconfigs(rootDir) {
  const fs = require('fs');
  const nodePath = require('path');

  // Directories to skip (mirrors ignoredDirs in indexer.go).
  const ignoredDirs = new Set([
    'node_modules', 'vendor', '.git', '.svn', '.hg',
    '.next', 'dist', 'build', '.turbo', 'out', 'coverage',
    '.yarn', '.pnp',
  ]);

  const results = [];

  function walk(dir) {
    let entries;
    try { entries = fs.readdirSync(dir, {withFileTypes: true}); }
    catch (e) { return; }

    for (const entry of entries) {
      if (entry.isDirectory()) {
        if (!ignoredDirs.has(entry.name) && !entry.name.startsWith('.')) {
          walk(nodePath.join(dir, entry.name));
        }
        continue;
      }
      if (entry.name !== 'tsconfig.json') continue;

      const tsconfigPath = nodePath.join(dir, entry.name);
      try {
        // Read and parse the tsconfig using the TS compiler API so that
        // extends chains and path resolution are handled correctly.
        const readResult = ts.readConfigFile(tsconfigPath, ts.sys.readFile);
        if (readResult.error) continue;

        const parsed = ts.parseJsonConfigFileContent(
          readResult.config,
          ts.sys,
          dir,
        );
        // Only skip on error-category diagnostics (category 0 = error).
        // Category 1 = warning (e.g. "No inputs found") is harmless — we
        // only need options.baseUrl and options.paths.
        const hasErrors = parsed.errors && parsed.errors.some(e => e.category === 0 /* error */);
        if (hasErrors) continue;

        const co = parsed.options;
        results.push({
          dir: dir,
          // baseUrl is already an absolute path after parseJsonConfigFileContent.
          baseUrl: co.baseUrl || null,
          // paths values are arrays of glob patterns; we keep them as-is.
          paths: co.paths || null,
        });
      } catch (e) {
        // Skip unparseable tsconfigs — fail open.
      }
    }
  }

  walk(rootDir);
  return results;
}

// JSONL protocol: read from stdin, write to stdout.
// Two message kinds:
//   {op:"tsconfig", root:"<absPath>"}  → tsconfig op (control)
//   {path:"...", content:"..."}        → file extraction (original protocol, back-compat)
const rl = readline.createInterface({input: process.stdin, terminal: false});
rl.on('line', (line) => {
  try {
    const msg = JSON.parse(line);
    if (msg.op === 'tsconfig') {
      // Control op: parse tsconfig paths for the given rootDir.
      const tsconfigs = parseTsconfigs(msg.root);
      process.stdout.write(JSON.stringify({tsconfigs}) + '\n');
    } else {
      // Original file-extraction protocol: {path, content}.
      const result = extractFile(msg.path, msg.content);
      process.stdout.write(JSON.stringify(result) + '\n');
    }
  } catch (e) {
    process.stdout.write(JSON.stringify({nodes: [], edges: [], unresolved_refs: [], errors: [{message: e.message, severity: 'error'}], duration_ms: 0}) + '\n');
  }
});
rl.on('close', () => process.exit(0));
