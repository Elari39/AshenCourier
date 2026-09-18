/**
 * ESLint 扁平配置（eslint.config.js，ESLint 9+）。
 *
 * 刻意只依赖 eslint-plugin-vue + typescript-eslint，不引 @vue/eslint-config-*：
 * 那两个包的价值主要就是「把 vue-eslint-parser 与 ts 解析器接起来」，
 * 也就是下面 parserOptions.parser 那一行 —— 少一层间接依赖，升级时少一处坑。
 */
import pluginVue from 'eslint-plugin-vue'
import tseslint from 'typescript-eslint'

export default tseslint.config(
  {
    name: 'app/ignores',
    ignores: ['dist/**', 'node_modules/**', 'coverage/**', '*.min.js'],
  },

  ...tseslint.configs.recommended,
  ...pluginVue.configs['flat/recommended'],

  {
    name: 'app/vue-ts-wiring',
    files: ['**/*.vue'],
    languageOptions: {
      parserOptions: {
        // vue-eslint-parser 解析 <script lang="ts">，内层交给 TS 解析器
        parser: tseslint.parser,
        extraFileExtensions: ['.vue'],
        ecmaVersion: 'latest',
        sourceType: 'module',
      },
    },
  },

  {
    name: 'app/rules',
    rules: {
      // 项目里大量使用 defineProps/defineEmits 的纯类型参数，未使用变量交给 TS 编译器把关
      '@typescript-eslint/no-unused-vars': ['error', { argsIgnorePattern: '^_' }],
      // 单文件组件名即文件名，不必强制多词
      'vue/multi-word-component-names': 'off',
      // 可选 prop 已经用 TS 的 `?` 表达「可以不传」，再要求写 `default: undefined`
      // 只是噪音；真正的默认值都在 withDefaults 里给了
      'vue/require-default-prop': 'off',
      // 属性换行交给 Prettier 决定
      'vue/max-attributes-per-line': 'off',
      'vue/singleline-html-element-content-newline': 'off',
      'vue/html-self-closing': [
        'error',
        { html: { void: 'always', normal: 'always', component: 'always' } },
      ],
    },
  },
)
