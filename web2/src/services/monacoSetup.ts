// 让 Monaco 编辑器从本地打包的 monaco-editor 加载,而非 @monaco-editor/react 默认的 CDN。
// 这样桌面 App / 离线环境也能正常使用插件代码编辑器。
//
// 通过 Vite 的 `?worker` 后缀把各语言 worker 一并打包;再用 loader.config({ monaco })
// 把 @monaco-editor/react 指向这份本地实例。该模块只需在使用 <Editor> 前以副作用方式
// 引入一次。
import { loader } from '@monaco-editor/react'
import * as monaco from 'monaco-editor'

// 插件编辑器只编辑 JavaScript,故仅打包 editor + typescript 两个 worker。
import editorWorker from 'monaco-editor/esm/vs/editor/editor.worker?worker'
import tsWorker from 'monaco-editor/esm/vs/language/typescript/ts.worker?worker'

// Monaco 通过全局 MonacoEnvironment.getWorker 决定如何创建各语言的 worker。
;(self as unknown as { MonacoEnvironment: monaco.Environment }).MonacoEnvironment = {
  getWorker(_workerId: string, label: string) {
    if (label === 'typescript' || label === 'javascript') {
      return new tsWorker()
    }
    return new editorWorker()
  },
}

// 指向本地 monaco,禁用 CDN 加载。
loader.config({ monaco })
