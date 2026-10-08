const { contextBridge, ipcRenderer } = require("electron");

contextBridge.exposeInMainWorld("artexDesktop", {
  status: () => ipcRenderer.invoke("desktop:status"),
  retry: () => ipcRenderer.invoke("desktop:retry"),
  quit: () => ipcRenderer.invoke("desktop:quit"),
  openChatGPTLogin: (url) => ipcRenderer.invoke("desktop:chatgpt-login", url),
  openChatGPTUsage: () => ipcRenderer.invoke("desktop:chatgpt-usage"),
});
