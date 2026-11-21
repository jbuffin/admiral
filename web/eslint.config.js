import babelParser from '@babel/eslint-parser';
import reactHooks from 'eslint-plugin-react-hooks';
import admiral from './eslint-rules.js';

// @typescript-eslint/parser needs the TypeScript JS compiler API, which
// TypeScript 7 does not ship, so Babel parses and every rule is untyped.
const parser = (isTSX) => ({
  parser: babelParser,
  parserOptions: {
    requireConfigFile: false,
    babelOptions: { presets: [['@babel/preset-typescript', { isTSX, allExtensions: true }]] },
    sourceType: 'module',
  },
});

const tests = ['src/**/*.test.{ts,tsx}'];

// Every rule is an error: a suppression records only errors.
export default [
  { files: ['src/**/*.ts'], languageOptions: parser(false) },
  { files: ['src/**/*.tsx'], languageOptions: parser(true) },
  {
    files: ['src/**/*.{ts,tsx}'],
    plugins: { 'react-hooks': reactHooks, admiral },
    linterOptions: { noInlineConfig: true },
    rules: {
      'react-hooks/rules-of-hooks': 'error',
      'react-hooks/exhaustive-deps': 'error',
    },
  },
  {
    files: ['src/**/*.{ts,tsx}'],
    ignores: tests,
    rules: { 'admiral/no-deferral': 'error' },
  },
  {
    files: ['src/**/*.tsx'],
    ignores: [...tests, 'src/app/hooks/**'],
    rules: { 'admiral/no-raw-effect': 'error' },
  },
];
