/**
 * inject-common-css.mjs
 *
 * Root-cause fix for duplicate CSS across 4 dashboard pages.
 * 1. Calls fill-html-head.mjs --replace-head to refresh token <head>
 * 2. Reads common.css and injects it as <style id="common-styles"> into <head>
 * 3. Removes the duplicate <style> block from <body>
 *
 * After this, all 4 pages share a single CSS source. Edit common.css only.
 */

import { readFileSync, writeFileSync } from 'fs';
import { execSync } from 'child_process';
import { join, dirname } from 'path';
import { fileURLToPath } from 'url';

const __dirname = dirname(fileURLToPath(import.meta.url));
const SKILL_DIR = 'c:\\Users\\86150\\.trae-cn\\builtin\\design\\default\\skills\\solo-design';
const PROJECT_DIR = join(__dirname, '..');
const COMMON_CSS_PATH = join(PROJECT_DIR, 'common.css');
const CSS_TOKEN_PATH = join(PROJECT_DIR, 'colors_and_type.css');

const pages = [
  { file: join(PROJECT_DIR, 'pages', 'overview.html'), title: '概览', prefix: 'alvus' },
  { file: join(PROJECT_DIR, 'pages', 'keys.html'), title: 'Key 管理', prefix: 'alvus' },
  { file: join(PROJECT_DIR, 'pages', 'logs.html'), title: '日志', prefix: 'alvus' },
  { file: join(PROJECT_DIR, 'pages', 'settings.html'), title: '设置', prefix: 'alvus' },
];

/* ── Step 1: Read common.css ── */
const commonCss = readFileSync(COMMON_CSS_PATH, 'utf-8');

/* ── Step 2: Process each page ── */
for (const page of pages) {
  const html = readFileSync(page.file, 'utf-8');

  // 2a: Run fill-html-head.mjs --replace-head to refresh token layer
  const cmd = `node "${join(SKILL_DIR, 'script', 'fill-html-head.mjs')}" "${CSS_TOKEN_PATH}" "${page.file}" --title="${page.title}" --lang="zh-CN" --prefix="${page.prefix}" --replace-head`;
  execSync(cmd, { stdio: 'pipe' });

  // 2b: Re-read after replace-head
  let updatedHtml = readFileSync(page.file, 'utf-8');

  // 2c: Inject common-styles after theme-vars
  const themeVarsEnd = '</style>';
  const themeVarsCloseIdx = updatedHtml.indexOf(themeVarsEnd);
  if (themeVarsCloseIdx === -1) {
    console.error(`[FAIL] ${page.file}: Could not find theme-vars </style>`);
    continue;
  }
  const injectPos = themeVarsCloseIdx + themeVarsEnd.length;
  const commonStyleBlock = `\n    <style id="common-styles">\n${commonCss}\n    </style>`;
  updatedHtml = updatedHtml.slice(0, injectPos) + commonStyleBlock + updatedHtml.slice(injectPos);

  // 2d: Remove body <style> block (the duplicate CSS)
  // Match the pattern: body <style> ... </style> immediately before <aside class="sidebar">
  updatedHtml = updatedHtml.replace(
    /[\s\S]*?<style>[\s\S]*?<\/style>\s*(?=<aside class="sidebar">)/,
    ''
  );

  writeFileSync(page.file, updatedHtml, 'utf-8');
  console.log(`[OK] ${page.file}`);
}

console.log('\nDone — common.css injected and body <style> removed for all 4 pages.');