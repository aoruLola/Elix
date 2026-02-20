import { contextBridge, ipcRenderer } from 'electron'

// 可以通过 contextBridge 将 Node.js API 暴露给渲染进程
contextBridge.exposeInMainWorld('electronAPI', {
    // 示例 IPC 方法
    onUpdate: (callback: any) => ipcRenderer.on('update', callback),
})
