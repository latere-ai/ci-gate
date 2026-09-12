// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: MIT

import assert from 'node:assert/strict';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { spawnSync } from 'node:child_process';
import { fileURLToPath } from 'node:url';
import test from 'node:test';
import { analyze } from './analyzer.mjs';

const directory = path.dirname(fileURLToPath(import.meta.url));
const analyzer = path.join(directory, 'analyzer.mjs');
const domain = `export enum Status { Running = 'running', Done = 'done' }
export enum Phase { First, Last }
export interface Session { status: Status; optional?: Status }
export function consume(s: Status): Status { return s }
export function consumePhase(s: Phase): Phase { return s }
`;
function fixture(t, files = {}, config = {}, dependencies = true) {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), 'ci-gate-tsenum-'));
  t.after(() => fs.rmSync(root, { recursive: true, force: true }));
  if (dependencies) fs.symlinkSync(path.resolve(directory, '../../node_modules'), path.join(root, 'node_modules'), 'dir');
  const entries = {
    'tsconfig.json': JSON.stringify({ compilerOptions: { strict: true, target: 'ES2022', module: 'ESNext', moduleResolution: 'Bundler', skipLibCheck: true }, include: ['**/*.ts', '**/*.vue'], ...config }),
    'domain.ts': domain,
    ...files,
  };
  for (const [name, content] of Object.entries(entries)) {
    const file = path.join(root, name);
    fs.mkdirSync(path.dirname(file), { recursive: true });
    fs.writeFileSync(file, content);
  }
  const project = { project: 'tsconfig.json', types: ['domain.ts#Status', 'domain.ts#Phase'] };
  return { root, projects: [project], project, check: () => analyze({ root, projects: [project] }) };
}
const has = (findings, pattern) => assert(findings.some(finding => pattern.test(finding)), `missing ${pattern} in\n${findings.join('\n')}`);

test('named members, import aliases, typed variables and optional fields pass', t => {
  const f = fixture(t, { 'main.ts': `import { Status as S, Phase, consume, Session } from './domain';
const first: S = S.Running;
let other: S = first;
other = S.Done;
consume(other);
const obj: Session = { status: S.Running, optional: undefined };
const arr: S[] = [S.Running, S.Done];
function identity(s: S): S { return s }
function cases(s: S) { switch(s) { case S.Running: break; case S.Done: break; default: break; } }
function phases(s: Phase) { switch(s) { case Phase.First: break; case Phase.Last: break; } }
const arbitrary: string = 'running'; const counter = 0;
` });
  f.project.fields = { 'domain.ts#Session.status': 'domain.ts#Status', 'domain.ts#Session.optional': 'domain.ts#Status' };
  assert.deepEqual(f.check(), []);
});

test('rejects primitive values at every typed use site, including numeric enums', t => {
  const f = fixture(t, { 'main.ts': `import { Status, Phase, consume, consumePhase, Session } from './domain';
let state: Phase = 0;
state = 1;
consumePhase(0);
const list: Phase[] = [0];
const object: { phase: Phase } = { phase: 0 };
function result(): Phase { return 0; }
const arrow: () => Phase = () => 0;
const asserted = 0 as Phase;
const casted = <Phase>0;
const checked = 0 satisfies Phase;
const laundered = 0 as unknown as Phase;
function compare(s: Phase) { return s === 0 || 1 === s || s < 1 || s != 0 || s >= 0; }
function text(s: Status) { return s === 'running' }
const bad: Session = { status: 'running' };
consume('running');
` });
  const findings = f.check();
  for (const line of [2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16]) has(findings, new RegExp(`main\\.ts:${line}:\\d+: use named members`));
  has(findings, /TypeScript TS/);
});

test('switches require all named members even with default and raw cases', t => {
  const f = fixture(t, { 'main.ts': `import { Phase, Status } from './domain';
function check(s: Phase) { switch(s) { case 0: return; default: return; } }
function empty(s: Status) { switch(s) { default: return; } }
function open(s: string) { switch(s) { case 'ok': return; } }
` });
  const findings = f.check();
  has(findings, /use named members.*Phase/);
  has(findings, /explicitly handle Phase.First, Phase.Last/);
  has(findings, /explicitly handle Status.Running, Status.Done/);
  assert(!findings.some(finding => finding.includes('main.ts:4:')));
});

test('fields require exact domain types and do not allow broad unions', t => {
  const f = fixture(t, { 'fields.ts': `import { Status, Phase } from './domain';
export interface Session { status: string; phase: Phase | number; optional?: Status | string; value: Status | undefined }
export type Alias = { status: Status };
export class Model { status: Status = Status.Running }
` });
  f.project.fields = {
    'fields.ts#Session.status': 'domain.ts#Status',
    'fields.ts#Session.phase': 'domain.ts#Phase',
    'fields.ts#Session.optional': 'domain.ts#Status',
    'fields.ts#Session.value': 'domain.ts#Status',
    'fields.ts#Alias.status': 'domain.ts#Status',
    'fields.ts#Model.status': 'domain.ts#Status',
  };
  const findings = f.check();
  assert.equal(findings.length, 4, findings.join('\n'));
  has(findings, /optional undefined allowed/);
});

test('parser exceptions permit conversions but do not waive switches or nested callbacks', t => {
  const f = fixture(t, { 'main.ts': `import { Status, Phase } from './domain';
export function parse(raw: number): Phase { return raw as Phase; }
export const parseArrow = (raw: string): Status => raw as Status;
export function inspect(s: Phase): Phase {
  const nested = () => { const illegal: Phase = 0; return illegal; };
  switch(s) { default: return 0 as Phase; }
}
export function unreviewed(raw: number): Phase { return raw as Phase; }
` });
  f.project.parsers = { 'main.ts#parse': 'validated API input', 'main.ts#parseArrow': 'validated legacy storage', 'main.ts#inspect': 'validated protocol' };
  const findings = f.check();
  has(findings, /main.ts:5:.*use named members/);
  has(findings, /switch must explicitly handle/);
  has(findings, /main.ts:8:.*use named members/);
  assert(!findings.some(finding => /main.ts:[23]:/.test(finding)));
});

test('ignores tests and declarations as implementation surfaces', t => {
  const f = fixture(t, {
    'example.test.ts': `import { Phase } from './domain'; const p: Phase = 0; const broken: string = 1;`,
    'example.spec.ts': `import { Phase } from './domain'; const p: Phase = 0;`,
    'tests/test.ts': `import { Phase } from '../domain'; const p: Phase = 0;`,
    'types.d.ts': 'declare const status: string;',
  });
  assert.deepEqual(f.check(), []);
});

test('compiler failures cannot turn into passing gates', t => {
  const f = fixture(t, { 'main.ts': `import missing from './missing'; const wrong: string = 1;` });
  const findings = f.check();
  has(findings, /Cannot find module '.\/missing'/);
  has(findings, /TypeScript TS2322/);
});

test('configuration errors explain the failing selector', t => {
  const f = fixture(t, { 'empty.ts': 'export enum Empty {}\nexport const value = 1;\nexport declare function absent(): void;\nexport const text: string = "a";' });
  const cases = [
    [{ types: ['bad'] }, /invalid selector/],
    [{ types: ['domain.ts#Missing'] }, /unknown symbol/],
    [{ types: ['missing.ts#Status'] }, /not a production file/],
    [{ types: ['../outside.ts#Status'] }, /must be inside repository/],
    [{ types: ['domain.ts#Status.member'] }, /not a native enum/],
    [{ types: ['empty.ts#value'] }, /not a native enum/],
    [{ types: ['empty.ts#Empty'] }, /no members/],
    [{ types: [] }, /at least one native enum/],
    [{ fields: { 'domain.ts#Session.status': 'empty.ts#Empty' } }, /unconfigured enum/],
    [{ fields: { 'domain.ts#Session': 'domain.ts#Status' } }, /field selector must/],
    [{ fields: { 'domain.ts#Status.Running': 'domain.ts#Status' } }, /field selector must/],
    [{ fields: { 'domain.ts#Session.missing': 'domain.ts#Status' } }, /unknown field/],
    [{ parsers: { 'domain.ts#consume': ' ' } }, /needs a reason/],
    [{ parsers: { 'empty.ts#value': 'not callable' } }, /not an implemented function/],
    [{ parsers: { 'empty.ts#absent': 'no body' } }, /not an implemented function/],
    [{ parsers: { 'domain.ts#consume.member': 'not a function selector' } }, /not an implemented function/],
    [{ project: 'missing.json' }, /tsconfig does not exist/],
    [{ project: '../outside.json' }, /project must be inside/],
  ];
  for (const [patch, pattern] of cases) has(analyze({ root: f.root, projects: [{ ...f.project, ...patch }] }), pattern);
  assert.throws(() => analyze({ root: f.root, projects: [] }), /at least one TypeScript project/);
});

test('missing dependencies and invalid tsconfigs fail', t => {
  const missing = fixture(t, {}, {}, false);
  has(missing.check(), /cannot load typescript.*frozen package install/);
  const malformed = fixture(t, { 'tsconfig.json': '{bad' });
  has(malformed.check(), /expected/);
  const invalid = fixture(t, {}, { compilerOptions: { target: 'invalid' } });
  has(invalid.check(), /Argument for '--target'/);
});

test('Vue script and script setup retain imported enum identity and original lines', t => {
  const f = fixture(t, { 'Page.vue': `<template><p>hello</p></template>\n<script setup lang="ts">\nimport { Phase } from './domain';\nconst state: Phase = 0;\nconst props = defineProps<{ phase: Phase }>();\n</script>`, 'main.ts': `import './Page.vue';` });
  const findings = f.check();
  has(findings, /Page.vue:4:.*use named members/);
  assert(!findings.some(finding => /Cannot find|not defined/.test(finding)), findings.join('\n'));
  fs.writeFileSync(path.join(f.root, 'Page.vue'), `<script lang="ts">\nimport { Phase } from './domain';\nconst state: Phase = Phase.First;\nexport default {};\n</script>`);
  assert.deepEqual(f.check(), []);
});

test('Vue parse errors, unsupported external scripts and missing compiler fail', t => {
  const f = fixture(t, { 'Page.vue': '<script lang="ts">const broken = 1;</script><script lang="ts">const other = 1;</script>' });
  has(f.check(), /only one <script>/);
  fs.writeFileSync(path.join(f.root, 'Page.vue'), '<script src="./domain.ts" lang="ts"></script>');
  has(f.check(), /external Vue script src is unsupported/);
  const missing = fixture(t, { 'Page.vue': '<script setup lang="ts">const a = 1;</script>' }, {}, false);
  fs.mkdirSync(path.join(missing.root, 'node_modules'));
  fs.symlinkSync(path.resolve(directory, '../../node_modules/typescript'), path.join(missing.root, 'node_modules/typescript'));
  has(missing.check(), /cannot load @vue\/compiler-sfc/);
});

test('standalone and embedded entry points fail then pass with real TypeScript and Vue projects', t => {
  const f = fixture(t, { 'Page.vue': '<script setup lang="ts">\nimport { Phase } from "./domain";\nconst phase: Phase = 0;\n</script>' });
  const input = JSON.stringify({ root: f.root, projects: f.projects });
  const fail = spawnSync(process.execPath, [analyzer], { input, encoding: 'utf8' });
  assert.equal(fail.status, 1);
  assert.match(fail.stderr, /Page.vue:3:.*use named members/);
  fs.writeFileSync(path.join(f.root, 'Page.vue'), '<script setup lang="ts">import { Phase } from "./domain"; const phase: Phase = Phase.First;</script>');
  const pass = spawnSync(process.execPath, ['--input-type=module', '--eval', fs.readFileSync(analyzer, 'utf8'), '--', '--enum-input', input], { encoding: 'utf8' });
  assert.equal(pass.status, 0, pass.stderr);
  assert.equal(pass.stderr, '');
  const malformed = spawnSync(process.execPath, [analyzer], { input: '{', encoding: 'utf8' });
  assert.equal(malformed.status, 1);
  assert.match(malformed.stderr, /enum-typescript:/);
});

test('parser exceptions only permit explicit conversions', t => {
  const f = fixture(t, { 'main.ts': `import { Phase } from './domain';
export function parse(raw: number): Phase {
  const bad: Phase = 0;
  if (bad === 0) return 1;
  return raw as Phase;
}
` });
  f.project.parsers = { 'main.ts#parse': 'validates input' };
  const findings = f.check();
  has(findings, /main.ts:3:.*use named members/);
  has(findings, /main.ts:4:.*use named members/);
  assert(!findings.some(finding => finding.includes('main.ts:5:')), findings.join('\n'));
});

test('primitive casts cannot erase enum identity before comparisons and switches', t => {
  const f = fixture(t, { 'main.ts': `import { Phase, Status } from './domain';
function compare(phase: Phase, status: Status) {
  const a = Number(phase) === 0;
  const b = 0 === (phase as number);
  const c = String(status) === 'running';
  switch (phase as number) { case Phase.First: break; default: break; }
  const serialized: string = String(status);
  return [a, b, c, serialized];
}
` });
  const findings = f.check();
  for (const line of [3, 4, 5]) has(findings, new RegExp(`main\\.ts:${line}:.*use named members`));
  has(findings, /explicitly handle Phase.Last/);
  assert(!findings.some(finding => finding.includes('main.ts:7:')), findings.join('\n'));
});

test('duplicate-valued enum members and typed constant aliases satisfy switches', t => {
  const f = fixture(t, { 'domain.ts': `export enum Phase { First = 0, Again = 0, Last = 1 }
const first = Phase.First;
export function check(p: Phase) { switch(p) { case first: break; case Phase.Last: break; } }
` });
  f.project.types = ['domain.ts#Phase'];
  assert.deepEqual(f.check(), []);
});

test('ambient declarations and imported Vue setup components remain available', t => {
  const f = fixture(t, {
    'globals.d.ts': 'declare const buildName: string;',
    'Page.vue': '<template>😀 {{ phase }}</template>\n<script setup lang="ts">\nimport { Phase } from "./domain";\nconst phase: Phase = Phase.First;\n</script>',
    'main.ts': 'import Page from "./Page.vue"; console.log(buildName, Page);',
  }, { compilerOptions: { strict: true, target: 'ES2022', module: 'ESNext', moduleResolution: 'Bundler', skipLibCheck: true, noUnusedLocals: true } });
  assert.deepEqual(f.check(), []);
});
