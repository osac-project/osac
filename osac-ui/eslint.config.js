import js from '@eslint/js'
import { defineConfig } from 'eslint/config'
import globals from 'globals'
import importPlugin from 'eslint-plugin-import'
import reactPlugin from 'eslint-plugin-react'
import reactHooks from 'eslint-plugin-react-hooks'
import tseslint from 'typescript-eslint'

export default defineConfig(
  {
    ignores: ['**/dist/**', '**/node_modules/**', 'libs/types/**'],
  },

  // Base: JS + TypeScript rules for all source files
  js.configs.recommended,
  ...tseslint.configs.recommendedTypeChecked,
  {
    files: ['**/*.{ts,tsx}'],
    plugins: {
      import: importPlugin,
    },
    languageOptions: {
      parserOptions: {
        project: true,
      },
    },
    rules: {
      'sort-imports': ['error', { ignoreDeclarationSort: true }],
      'import/order': [
        'error',
        {
          alphabetize: {
            caseInsensitive: true,
            order: 'asc',
          },
          groups: [
            ['builtin', 'external'],
            'internal',
            ['parent', 'sibling', 'index'],
            'object',
          ],
          'newlines-between': 'always',
          pathGroups: [
            {
              pattern: 'react',
              group: 'external',
              position: 'before',
            },
            {
              pattern: 'react-*',
              group: 'external',
              position: 'before',
            },
            {
              pattern: '@osac/**',
              group: 'internal',
            },
            {
              pattern: '*.{css,scss,sass,less,svg,png,jpg,jpeg,gif,webp}',
              group: 'object',
              patternOptions: { matchBase: true },
            },
          ],
          pathGroupsExcludedImportTypes: ['builtin'],
          distinctGroup: false,
          warnOnUnassignedImports: true,
        },
      ],
      'no-console': 'error',
      '@typescript-eslint/explicit-function-return-type': 'off',
      '@typescript-eslint/no-misused-promises': 'off',
      '@typescript-eslint/no-floating-promises': 'off',
      '@typescript-eslint/no-unsafe-enum-comparison': 'off',
      '@typescript-eslint/no-unused-vars': ['error', { argsIgnorePattern: '^_', varsIgnorePattern: '^_' }],
      'prefer-arrow-callback': 'error',
      'func-style': ['error', 'expression', { allowArrowFunctions: true }],
      'semi': ['error', 'always'],
      'curly': ['error', 'all'],
      '@typescript-eslint/no-non-null-assertion': 'error',
    },
  },

  // Frontend + UI components: React rules
  {
    files: ['apps/app-frontend/src/**/*.{ts,tsx}', 'libs/ui-components/src/**/*.{ts,tsx}'],
    extends: [reactPlugin.configs.flat.recommended, reactPlugin.configs.flat['jsx-runtime']],
    languageOptions: {
      globals: { ...globals.browser },
    },
    plugins: {
      'react-hooks': reactHooks,
    },
    settings: {
      react: {
        version: 'detect',
      },
    },
    rules: {
      'react/self-closing-comp': 'error',
      'react-hooks/rules-of-hooks': 'error',
      'react-hooks/exhaustive-deps': 'error',
      'react/prop-types': 'off',
      // New react-hooks v7 rules – code intentionally uses these patterns
      'react-hooks/refs': 'off',
      'react-hooks/set-state-in-effect': 'off',
      'no-restricted-imports': [
        'error',
        {
          paths: [
            {
              name: '@patternfly/react-core',
              importNames: ['Form'],
              message: 'Use OsacForm wrapper',
            },
            {
              name: '@patternfly/react-icons',
              message:
                'Use a deep ESM import: @patternfly/react-icons/dist/esm/icons/<icon-name>',
            },
            {
              name: '@patternfly/react-tokens',
              message:
                'Use a deep ESM import: @patternfly/react-tokens/dist/esm/<token-name>',
            },
            {
              name: 'lodash-es',
              message: 'Import using full path `lodash-es/<function>` instead',
            },
            {
              name: 'react-i18next',
              importNames: ['useTranslation'],
              message:
                "Import useTranslation from '@osac/ui-components/hooks/useTranslation' instead",
            },
          ],
        },
      ],
    },
  },
  // ui-components consumers must use the API wrappers instead of TanStack hooks directly
  {
    files: ['libs/ui-components/src/**/*.{ts,tsx}'],
    ignores: [
      'libs/ui-components/src/api/use-resource.ts',
      'libs/ui-components/src/api/use-api-query.ts',
      'libs/ui-components/src/hooks/useTranslation.ts',
    ],
    rules: {
      'no-restricted-imports': [
        'error',
        {
          paths: [
            {
              name: '@patternfly/react-core',
              importNames: ['Form'],
              message: 'Use OsacForm wrapper',
            },
            {
              name: '@patternfly/react-icons',
              message:
                'Use a deep ESM import: @patternfly/react-icons/dist/esm/icons/<icon-name>',
            },
            {
              name: '@patternfly/react-tokens',
              message:
                'Use a deep ESM import: @patternfly/react-tokens/dist/esm/<token-name>',
            },
            {
              name: 'lodash-es',
              message: 'Import using full path `lodash-es/<function>` instead',
            },
            {
              name: '@tanstack/react-query',
              importNames: ['useQuery'],
              message:
                "Use useListResource, useListAllResources, or useGetResource from '@osac/ui-components/api/use-resource' instead.",
            },
            {
              name: '@tanstack/react-query',
              importNames: ['useQueryClient'],
              message:
                "Use useInvalidateServiceQueries from '@osac/ui-components/api/use-resource' to invalidate resource queries.",
            },
            {
              name: 'react-i18next',
              importNames: ['useTranslation'],
              message:
                "Import useTranslation from '@osac/ui-components/hooks/useTranslation' instead",
            },
          ],
        },
      ],
    },
  },
)
