// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: MIT

import fs from 'node:fs';
import path from 'node:path';
import { createRequire } from 'node:module';
import { fileURLToPath } from 'node:url';

const excluded = file => /(?:^|[/\\])(?:node_modules|__tests__|tests|testdata)(?:[/\\]|$)|(?:\.test|\.spec)\.[cm]?[jt]sx?$|\.d\.[cm]?ts$/.test(file);
const inside = (root, file) => {
  const relative = path.relative(root, file);
  return relative !== '..' && !relative.startsWith(`..${path.sep}`) && !path.isAbsolute(relative);
};

// Dependencies resolve from the consumer's tsconfig, never from this embedded script.
function dependency(require, name) {
  try {
    return require(name);
  } catch (error) {
    throw new Error(`cannot load ${name} from the project's installed dependencies; run its frozen package install (${error.message})`);
  }
}

function projectProgram(root, project) {
  const configFile = path.resolve(root, project.project);
  if (!inside(root, configFile)) throw new Error(`project must be inside repository: ${project.project}`);
  if (!fs.existsSync(configFile)) throw new Error(`tsconfig does not exist: ${project.project}`);
  const base = path.dirname(configFile);
  const require = createRequire(configFile);
  const ts = dependency(require, 'typescript');
  const config = ts.readConfigFile(configFile, ts.sys.readFile);
  if (config.error) throw new Error(ts.flattenDiagnosticMessageText(config.error.messageText, '\n'));
  const parsed = ts.parseJsonConfigFileContent(config.config, ts.sys, base, undefined, configFile, undefined,
    [{ extension: '.vue', isMixedContent: true, scriptKind: ts.ScriptKind.Deferred }]);
  if (parsed.errors.length) throw new Error(parsed.errors.map(d => ts.flattenDiagnosticMessageText(d.messageText, '\n')).join('\n'));
  const virtual = new Map();
  const vueFiles = parsed.fileNames.filter(file => file.endsWith('.vue') && !excluded(file));
  if (vueFiles.length) {
    const sfc = dependency(require, '@vue/compiler-sfc');
    for (const file of vueFiles) {
      const text = fs.readFileSync(file, 'utf8');
      const { descriptor, errors } = sfc.parse(text, { filename: file });
      if (errors.length) throw new Error(`${path.relative(root, file)}: ${errors.map(String).join('\n')}`);
      const blocks = [descriptor.script, descriptor.scriptSetup].filter(Boolean);
      let script = text.replace(/[^\r\n]/g, ' ');
      for (const block of blocks) {
        if (block.src) throw new Error(`${path.relative(root, file)}: external Vue script src is unsupported; include its TypeScript file directly`);
        const start = block.loc.start.offset;
        script = script.slice(0, start) + block.content + script.slice(start + block.content.length);
      }
      // These are compiler macros in script setup. Importing only used macros
      // gives their real Vue types without fabricating an ambient any boundary.
      const macros = ['defineProps', 'defineEmits', 'defineExpose', 'defineOptions', 'defineSlots', 'defineModel', 'withDefaults'];
      const used = macros.filter(name => new RegExp(`\\b${name}\\s*(?:<|\\()`).test(script));
      if (used.length) script += `\nimport { ${used.join(', ')} } from 'vue';\n`;
      // script setup becomes a component with a default export at compilation.
      // Its template is outside this gate; preserve module imports here while
      // vue-tsc continues to own template and component-prop checking.
      const syntax = ts.createSourceFile(file, script, ts.ScriptTarget.Latest, true);
      if (!syntax.statements.some(statement => ts.isExportAssignment(statement) && !statement.isExportEquals)) {
        script += '\nexport default {};\n';
      }
      virtual.set(`${file}.ts`, script);
    }
  }
  const options = { ...parsed.options, noEmit: true };
  const host = ts.createCompilerHost(options);
  const originalRead = host.readFile;
  const originalExists = host.fileExists;
  host.readFile = file => virtual.get(file) ?? originalRead(file);
  host.fileExists = file => virtual.has(file) || originalExists(file);
  host.getSourceFile = (file, languageVersion) => {
    const text = host.readFile(file);
    return text === undefined ? undefined : ts.createSourceFile(file, text, languageVersion, true);
  };
  host.resolveModuleNames = (names, containingFile) => names.map(name => {
    if (name.endsWith('.vue')) {
      const resolved = path.resolve(path.dirname(containingFile), `${name}.ts`);
      if (virtual.has(resolved)) return { resolvedFileName: resolved, extension: ts.Extension.Ts };
    }
    return ts.resolveModuleName(name, containingFile, options, host).resolvedModule;
  });
  const fileNames = parsed.fileNames.filter(file => !excluded(file) || /\.d\.[cm]?ts$/.test(file)).map(file => file.endsWith('.vue') ? `${file}.ts` : file);
  const program = ts.createProgram(fileNames, options, host);
  return { ts, base, program };
}

function inspectProject(root, project) {
  const { ts, base, program } = projectProgram(root, project);
  const checker = program.getTypeChecker();
  const findings = new Set();
  const location = node => {
    const source = node.getSourceFile();
    const point = source.getLineAndCharacterOfPosition(node.getStart());
    return `${path.relative(root, source.fileName).replace(/\.vue\.ts$/, '.vue')}:${point.line + 1}:${point.character + 1}`;
  };
  const report = (node, message) => findings.add(`${location(node)}: ${message}`);
  const sourceFiles = program.getSourceFiles().filter(file => !file.isDeclarationFile && !excluded(file.fileName) && inside(root, file.fileName));
  const canonical = symbol => symbol && symbol.flags & ts.SymbolFlags.Alias ? checker.getAliasedSymbol(symbol) : symbol;
  const resolveSelector = selector => {
    const match = /^(.+)#([A-Za-z_$][\w$]*)(?:\.([A-Za-z_$][\w$]*))?$/.exec(selector);
    if (!match) throw new Error(`invalid selector ${selector}; expected file.ts#Symbol or file.ts#Interface.field`);
    const file = path.resolve(base, match[1]);
    if (!inside(root, file)) throw new Error(`selector must be inside repository: ${selector}`);
    const source = sourceFiles.find(source => source.fileName === file || source.fileName === `${file}.ts`);
    if (!source) throw new Error(`selector source is not a production file in the project: ${selector}`);
    const declarations = source.statements.flatMap(statement => ts.isVariableStatement(statement) ? [...statement.declarationList.declarations] : [statement]);
    const declaration = declarations.find(node => node.name && ts.isIdentifier(node.name) && node.name.text === match[2]);
    if (!declaration) throw new Error(`unknown symbol: ${selector}`);
    return { declaration, field: match[3] };
  };
  const domains = new Map();
  for (const selector of project.types ?? []) {
    const { declaration, field } = resolveSelector(selector);
    if (field || !ts.isEnumDeclaration(declaration)) throw new Error(`configured domain is not a native enum: ${selector}`);
    if (!declaration.members.length) throw new Error(`enum has no members: ${selector}`);
    const symbol = checker.getSymbolAtLocation(declaration.name);
    const values = new Map();
    const members = new Map();
    for (const member of declaration.members) {
      const value = checker.getConstantValue(member);
      if (value === undefined) throw new Error(`enum member must have a constant value: ${selector}.${member.name.getText()}`);
      const key = `${typeof value}:${value}`;
      const memberSymbol = checker.getSymbolAtLocation(member.name);
      members.set(memberSymbol, key);
      if (!values.has(key)) values.set(key, memberSymbol);
    }
    domains.set(symbol, { selector, symbol, declaration, values, members });
  }
  if (!domains.size) throw new Error('at least one native enum must be configured in types');
  const domainForSymbol = symbol => {
    symbol = canonical(symbol);
    if (domains.has(symbol)) return domains.get(symbol);
    const member = symbol?.declarations?.find(node => ts.isEnumMember(node));
    return member ? domains.get(checker.getSymbolAtLocation(member.parent.name)) : undefined;
  };
  const parts = type => type.isUnion() ? type.types : [type];
  const domainsForType = type => new Set(parts(type).map(part => domainForSymbol(part.symbol)).filter(Boolean));
  const exactDomain = (type, domain, optional = false) => {
    const types = parts(type).filter(part => !(optional && part.flags & ts.TypeFlags.Undefined));
    return types.length > 0 && types.every(part => domainForSymbol(part.symbol) === domain);
  };
  const parserNodes = new Set();
  for (const [selector, reason] of Object.entries(project.parsers ?? {})) {
    if (typeof reason !== 'string' || !reason.trim()) throw new Error(`parser boundary needs a reason: ${selector}`);
    const { declaration, field } = resolveSelector(selector);
    const fn = ts.isVariableDeclaration(declaration) ? declaration.initializer : declaration;
    if (field || !fn || !ts.isFunctionLike(fn) || !fn.body) throw new Error(`parser is not an implemented function: ${selector}`);
    parserNodes.add(fn);
  }
  for (const [selector, target] of Object.entries(project.fields ?? {})) {
    const domain = [...domains.values()].find(domain => domain.selector === target);
    if (!domain) throw new Error(`field ${selector} references unconfigured enum: ${target}`);
    const { declaration, field } = resolveSelector(selector);
    if (!field || !(ts.isInterfaceDeclaration(declaration) || ts.isTypeAliasDeclaration(declaration) || ts.isClassDeclaration(declaration))) {
      throw new Error(`field selector must name Interface.field, Type.field, or Class.field: ${selector}`);
    }
    const type = checker.getTypeAtLocation(declaration.name);
    const property = checker.getPropertyOfType(type, field);
    if (!property) throw new Error(`unknown field: ${selector}`);
    const propertyType = checker.getTypeOfSymbolAtLocation(property, declaration);
    const optional = Boolean(property.flags & ts.SymbolFlags.Optional);
    if (!exactDomain(propertyType, domain, optional)) report(declaration, `${selector} must use exactly ${target}${optional ? ' (optional undefined allowed)' : ''}; got ${checker.typeToString(propertyType)}`);
  }
  const parserFor = node => {
    for (let parent = node.parent; parent; parent = parent.parent) {
      if (ts.isFunctionLike(parent)) return parserNodes.has(parent);
    }
    return false;
  };
  const checkValue = (expression, expected) => {
    if (!expression || !expected) return;
    const parent = expression.parent;
    if (parent && (ts.isAsExpression(parent) || ts.isTypeAssertionExpression(parent)) && parent.expression === expression && parserFor(parent)) return;
    const expectedDomains = domainsForType(expected);
    if (!expectedDomains.size) return;
    const actual = checker.getTypeAtLocation(expression);
    const invalid = parts(actual).filter(type => !(type.flags & (ts.TypeFlags.Undefined | ts.TypeFlags.Null | ts.TypeFlags.Never)))
      .some(type => !expectedDomains.has(domainForSymbol(type.symbol)));
    if (invalid) report(expression, `use named members of ${[...expectedDomains].map(domain => domain.selector).join(' or ')} instead of a primitive value`);
  };
  const uncast = expression => {
    while (true) {
      if (ts.isParenthesizedExpression(expression) || ts.isAsExpression(expression) || ts.isTypeAssertionExpression(expression)) {
        expression = expression.expression;
      } else if (ts.isCallExpression(expression) && expression.arguments.length === 1 && ts.isIdentifier(expression.expression)
        && ['String', 'Number'].includes(expression.expression.text)
        && checker.getSymbolAtLocation(expression.expression)?.declarations?.every(declaration => declaration.getSourceFile().isDeclarationFile)) {
        expression = expression.arguments[0];
      } else return expression;
    }
  };
  const comparisonOperators = new Set([
    ts.SyntaxKind.EqualsEqualsToken, ts.SyntaxKind.EqualsEqualsEqualsToken, ts.SyntaxKind.ExclamationEqualsToken,
    ts.SyntaxKind.ExclamationEqualsEqualsToken, ts.SyntaxKind.LessThanToken, ts.SyntaxKind.LessThanEqualsToken,
    ts.SyntaxKind.GreaterThanToken, ts.SyntaxKind.GreaterThanEqualsToken,
  ]);
  const inspect = node => {
    // Initializers inside an enum define the wire values, rather than using them.
    if (ts.isEnumDeclaration(node)) return;
    if (ts.isExpression(node)) checkValue(node, checker.getContextualType(node));
    if (ts.isBinaryExpression(node)) {
      if (comparisonOperators.has(node.operatorToken.kind)) {
        checkValue(uncast(node.left), checker.getTypeAtLocation(uncast(node.right)));
        checkValue(uncast(node.right), checker.getTypeAtLocation(uncast(node.left)));
      } else if (node.operatorToken.kind >= ts.SyntaxKind.FirstAssignment && node.operatorToken.kind <= ts.SyntaxKind.LastAssignment) {
        checkValue(node.right, checker.getTypeAtLocation(node.left));
      }
    }
    if (ts.isAsExpression(node) || ts.isTypeAssertionExpression(node) || ts.isSatisfiesExpression(node)) {
      if (!parserFor(node) || ts.isSatisfiesExpression(node)) checkValue(node.expression, checker.getTypeAtLocation(node.type));
    }
    if (ts.isSwitchStatement(node)) {
      const type = checker.getTypeAtLocation(uncast(node.expression));
      for (const domain of domainsForType(type)) {
        const seen = new Set();
        for (const clause of node.caseBlock.clauses) {
          if (!ts.isCaseClause(clause)) continue;
          const expression = uncast(clause.expression);
          checkValue(expression, type);
          const actual = checker.getTypeAtLocation(expression);
          // A const alias retains the enum member's literal type even though
          // the expression's symbol names the variable rather than the member.
          const symbol = canonical(actual.symbol);
          if (domain.members.has(symbol)) seen.add(domain.members.get(symbol));
        }
        const missing = [...domain.values].filter(([value]) => !seen.has(value)).map(([, member]) => `${domain.declaration.name.text}.${member.name}`);
        if (missing.length) report(node, `switch must explicitly handle ${missing.join(', ')}; default does not cover enum members`);
      }
    }
    ts.forEachChild(node, inspect);
  };
  for (const source of sourceFiles) inspect(source);
  for (const diagnostic of ts.getPreEmitDiagnostics(program)) {
    if (diagnostic.file && excluded(diagnostic.file.fileName)) continue;
    // Template references are not represented by the script projection. These
    // diagnostics remain vue-tsc's responsibility, not evidence of a bad enum.
    if (diagnostic.file?.fileName.endsWith('.vue.ts') && [6133, 6192, 6196, 6198, 6199].includes(diagnostic.code)) continue;
    const message = ts.flattenDiagnosticMessageText(diagnostic.messageText, '\n');
    if (diagnostic.file && diagnostic.start !== undefined) {
      const point = diagnostic.file.getLineAndCharacterOfPosition(diagnostic.start);
      findings.add(`${path.relative(root, diagnostic.file.fileName).replace(/\.vue\.ts$/, '.vue')}:${point.line + 1}:${point.character + 1}: TypeScript TS${diagnostic.code}: ${message}`);
    } else findings.add(`TypeScript TS${diagnostic.code}: ${message}`);
  }
  return [...findings];
}

// Analyze returns deterministic diagnostics and does not modify source files.
export function analyze(input) {
  const root = path.resolve(input.root);
  if (!Array.isArray(input.projects) || !input.projects.length) throw new Error('at least one TypeScript project is required');
  return input.projects.flatMap(project => {
    try {
      return inspectProject(root, project);
    } catch (error) {
      return [`${project.project}: ${error.message}`];
    }
  }).sort();
}

// The Go wrapper passes JSON as an argument; direct script invocations read stdin.
if (process.argv[1] === '--enum-input' || fileURLToPath(import.meta.url) === path.resolve(process.argv[1] ?? '')) {
  try {
    const input = JSON.parse(process.argv[1] === '--enum-input' ? process.argv[2] : fs.readFileSync(0, 'utf8'));
    const findings = analyze(input);
    for (const finding of findings) console.error(finding);
    if (findings.length) process.exitCode = 1;
  } catch (error) {
    console.error(`enum-typescript: ${error.message}`);
    process.exitCode = 1;
  }
}
