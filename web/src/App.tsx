import { ThemeProvider } from "@/components/ThemeProvider";
import { ThemeToggle } from "@/components/ThemeToggle";

export default function App() {
  return (
    <ThemeProvider>
      <div className="min-h-screen flex flex-col items-center justify-center gap-6">
        <div className="text-sm tracking-widest uppercase font-mono text-fg-secondary">
          Navaris — scaffold OK
        </div>
        <ThemeToggle />
      </div>
    </ThemeProvider>
  );
}
