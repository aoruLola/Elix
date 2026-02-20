import { BrowserWindow as e, app as t } from "electron";
import * as n from "path";
import { fileURLToPath as r } from "url";
var i = r(import.meta.url), a = n.dirname(i);
function o() {
	let t = new e({
		width: 1200,
		height: 800,
		webPreferences: {
			preload: n.join(a, "preload.mjs"),
			nodeIntegration: !1,
			contextIsolation: !0
		}
	});
	process.env.VITE_DEV_SERVER_URL ? (t.loadURL(process.env.VITE_DEV_SERVER_URL), t.webContents.openDevTools()) : t.loadFile(n.join(a, "../dist/index.html"));
}
t.whenReady().then(() => {
	o(), t.on("activate", () => {
		e.getAllWindows().length === 0 && o();
	});
}), t.on("window-all-closed", () => {
	process.platform !== "darwin" && t.quit();
});
