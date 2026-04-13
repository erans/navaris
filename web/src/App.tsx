import { Suspense } from "react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { RouterProvider } from "react-router-dom";
import { Toaster } from "sonner";
import { ThemeProvider } from "@/components/ThemeProvider";
import { ErrorBoundary } from "@/components/ErrorBoundary";
import { router } from "@/router";

// Single QueryClient for the app. Defaults: retry once, refetch on window
// focus disabled (we drive invalidations off the event stream instead).
const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      retry: 1,
      refetchOnWindowFocus: false,
      staleTime: 30_000,
    },
  },
});

export default function App() {
  return (
    <ErrorBoundary>
      <ThemeProvider>
        <QueryClientProvider client={queryClient}>
          {/*
            Every route element is a React.lazy(), so the initial render of
            a deep-linked URL suspends while its chunk loads. React Router's
            data router does NOT wrap element rendering in Suspense for us
            — on a cold reload of /sandboxes we'd otherwise hit minified
            React error #426 ("a component suspended while responding to
            synchronous input") and the errorElement would catch it.
            A single top-level boundary with a null fallback keeps the
            pre-hydration flash invisible (chunks load in well under a
            frame on localhost) while still letting React resolve the
            lazy import before committing the tree.
          */}
          <Suspense fallback={null}>
            <RouterProvider router={router} />
          </Suspense>
          <Toaster
            position="bottom-right"
            toastOptions={{
              className: "!font-sans !text-sm",
            }}
          />
        </QueryClientProvider>
      </ThemeProvider>
    </ErrorBoundary>
  );
}
