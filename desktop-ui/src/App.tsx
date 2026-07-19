import { useEffect } from "react";
import { Routes, Route } from "react-router-dom";
import { Sidebar } from "./components/layout/sidebar";
import { Toaster } from "./components/ui/toaster";
import { reloadConfigWithToast } from "./lib/app-actions";
import { Overview } from "./pages/Overview";
import { Sessions } from "./pages/Sessions";
import { RequestLogs } from "./pages/RequestLogs";
import { Logs } from "./pages/Logs";
import { TraceViewer } from "./pages/TraceViewer";
import { Providers } from "./pages/Providers";
import { Models } from "./pages/Models";
import { Routes as RoutesPage } from "./pages/Routes";
import { Settings } from "./pages/Settings";
import { Playground } from "./pages/Playground";
import { Prompts } from "./pages/Prompts";

export default function App() {
  // Global reload-config shortcut, replacing the native menu's Ctrl/Cmd+R
  // (the menu bar only exists on macOS now). preventDefault so the webview
  // doesn't also treat it as a page refresh.
  useEffect(() => {
    function onKeyDown(e: KeyboardEvent) {
      if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === "r") {
        e.preventDefault();
        void reloadConfigWithToast();
      }
    }
    window.addEventListener("keydown", onKeyDown);
    return () => window.removeEventListener("keydown", onKeyDown);
  }, []);

  return (
    <div className="flex h-screen w-screen overflow-hidden bg-background text-foreground">
      <Sidebar />
      <main className="flex-1 overflow-auto bg-background">
        <Routes>
          <Route path="/" element={<Overview />} />
          <Route path="/sessions" element={<Sessions />} />
          <Route path="/request-logs" element={<RequestLogs />} />
          <Route path="/logs" element={<Logs />} />
          <Route path="/trace/:sessionId" element={<TraceViewer />} />
          <Route path="/providers" element={<Providers />} />
          <Route path="/models" element={<Models />} />
          <Route path="/routes" element={<RoutesPage />} />
          <Route path="/settings" element={<Settings />} />
          <Route path="/playground" element={<Playground />} />
          <Route path="/prompts" element={<Prompts />} />
        </Routes>
      </main>
      <Toaster />
    </div>
  );
}
