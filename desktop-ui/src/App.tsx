import { Routes, Route } from "react-router-dom";
import { Sidebar } from "./components/layout/sidebar";
import { Overview } from "./pages/Overview";
import { Sessions } from "./pages/Sessions";
import { TraceViewer } from "./pages/TraceViewer";
import { Providers } from "./pages/Providers";
import { Models } from "./pages/Models";
import { Routes as RoutesPage } from "./pages/Routes";

export default function App() {
  return (
    <div className="flex h-screen w-screen overflow-hidden bg-background text-foreground">
      <Sidebar />
      <main className="flex-1 overflow-auto">
        <Routes>
          <Route path="/" element={<Overview />} />
          <Route path="/sessions" element={<Sessions />} />
          <Route path="/trace/:sessionId" element={<TraceViewer />} />
          <Route path="/providers" element={<Providers />} />
          <Route path="/models" element={<Models />} />
          <Route path="/routes" element={<RoutesPage />} />
        </Routes>
      </main>
    </div>
  );
}
