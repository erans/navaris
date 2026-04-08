import { createBrowserRouter, Navigate } from "react-router-dom";
import { lazy, type ReactNode } from "react";
import { Placeholder } from "@/routes/Placeholder";
import { RequireAuth } from "@/components/RequireAuth";
import { AppShell } from "@/components/AppShell";

const Login = lazy(() => import("@/routes/Login"));
const Sandboxes = lazy(() => import("@/routes/Sandboxes"));

function shell(element: ReactNode) {
  return (
    <RequireAuth>
      <AppShell>{element}</AppShell>
    </RequireAuth>
  );
}

export const router = createBrowserRouter(
  [
    { path: "/login", element: <Login /> },
    { path: "/", element: shell(<Navigate to="/sandboxes" replace />) },
    { path: "/projects", element: shell(<Placeholder label="projects" />) },
    { path: "/sandboxes", element: shell(<Sandboxes />) },
    { path: "/sandboxes/:id", element: shell(<Placeholder label="sandbox detail" />) },
    { path: "/events", element: shell(<Placeholder label="events" />) },
    { path: "*", element: <Navigate to="/" replace /> },
  ],
);
