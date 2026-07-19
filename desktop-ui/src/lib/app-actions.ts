import { toast } from "sonner";
import { quitApp, reloadConfig, revealConfigFolder } from "./api";

// App-level actions shared by the sidebar footer buttons and the global
// Ctrl/Cmd+R shortcut. They replace the native menu items, which only exist
// on macOS now (Windows/Linux have no menu bar).

export async function reloadConfigWithToast(): Promise<void> {
  try {
    const res = await reloadConfig();
    toast.success("配置已重载。");
    if (res.warning) toast.warning(res.warning);
  } catch (e) {
    toast.error(String((e as Error)?.message ?? e));
  }
}

export async function revealConfigFolderWithToast(): Promise<void> {
  try {
    await revealConfigFolder();
    toast.success("已在文件管理器中打开。");
  } catch (e) {
    toast.error(String((e as Error)?.message ?? e));
  }
}

export async function quitAppWithToast(): Promise<void> {
  try {
    await quitApp();
    // The process exits right after answering; this toast is mostly a
    // "your click worked" signal for the brief moment before the window goes.
    toast.success("正在退出…");
  } catch (e) {
    toast.error(String((e as Error)?.message ?? e));
  }
}
