/// <reference types="vite/client" />

declare module '*.vue' {
  import type { DefineComponent } from 'vue'
  const component: DefineComponent<Record<string, unknown>, Record<string, unknown>, unknown>
  export default component
}

// @wangeditor/editor-for-vue@5.1.12 的 package.json exports 未声明 types，
// bundler 解析下取不到 dist/src/index.d.ts；这里补最小声明（具体 props 由
// wangEditor 官方类型在调用侧约束）。
declare module '@wangeditor/editor-for-vue' {
  import type { DefineComponent } from 'vue'
  export const Editor: DefineComponent<Record<string, unknown>, Record<string, unknown>, unknown>
  export const Toolbar: DefineComponent<Record<string, unknown>, Record<string, unknown>, unknown>
}
