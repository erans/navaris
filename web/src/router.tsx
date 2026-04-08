import { createBrowserRouter, Navigate } from "react-router-dom";
import { lazy, type ReactNode } from "react";
import { RequireAuth } from "@/components/RequireAuth";
import { AppShell } from "@/components/AppShell";
import { RouteError } from "@/components/RouteError";

const Login = lazy(() => import("@/routes/Login"));
const Sandboxes = lazy(() => import("@/routes/Sandboxes"));
const SandboxDetail = lazy(() => import("@/routes/SandboxDetail"));
const Projects = lazy(() => import("@/routes/Projects"));
const Events = lazy(() => import("@/routes/Events"));
const Terminal = lazy(() => import("@/routes/Terminal"));

function shell(element: ReactNode) {
  return (
    <RequireAuth>
      <AppShell>{element}</AppShell>
    </RequireAuth>
  );
}

// Shell-wrapped error element keeps the sidebar and status line visible when
// a protected route throws, so the user can navigate away without reloading.
const shelledError = shell(<RouteError />);

export const router = createBrowserRouter(
  [
    { path: "/login", element: <Login />, errorElement: <RouteError /> },
    { path: "/", element: shell(<Navigate to="/sandboxes" replace />), errorElement: shelledError },
    { path: "/projects", element: shell(<Projects />), errorElement: shelledError },
    { path: "/sandboxes", element: shell(<Sandboxes />), errorElement: shelledError },
    { path: "/sandboxes/:id", element: shell(<SandboxDetail />), errorElement: shelledError },
    { path: "/sandboxes/:id/terminal", element: shell(<Terminal />), errorElement: shelledError },
    { path: "/events", element: shell(<Events />), errorElement: shelledError },
    { path: "*", element: <Navigate to="/" replace /> },
  ],
);
