import { ThemeProvider as NextThemesProvider } from "next-themes";
import type { ReactNode } from "react";

interface Props {
  children: ReactNode;
}

// ThemeProvider wraps next-themes with our project defaults:
// - data-theme attribute (matches the CSS tokens in index.css)
// - localStorage key "navaris-theme" (matches the pre-hydration script in index.html)
// - system default
export function ThemeProvider({ children }: Props) {
  return (
    <NextThemesProvider
      attribute="data-theme"
      defaultTheme="system"
      enableSystem
      storageKey="navaris-theme"
      disableTransitionOnChange
    >
      {children}
    </NextThemesProvider>
  );
}
