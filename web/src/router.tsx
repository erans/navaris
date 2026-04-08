import { createBrowserRouter, Navigate } from "react-router-dom";
import { lazy, type ReactNode } from "react";
import { RequireAuth } from "@/components/RequireAuth";
import { AppShell } from "@/components/AppShell";

const Login = lazy(() => import("@/routes/Login"));
const Sandboxes = lazy(() => import("@/routes/Sandboxes"));
const SandboxDetail = lazy(() => import("@/routes/SandboxDetail"));
const Projects = lazy(() => import("@/routes/Projects"));
const Events = lazy(() => import("@/routes/Events"));

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
    { path: "/projects", element: shell(<Projects />) },
    { path: "/sandboxes", element: shell(<Sandboxes />) },
    { path: "/sandboxes/:id", element: shell(<SandboxDetail />) },
    { path: "/events", element: shell(<Events />) },
    { path: "*", element: <Navigate to="/" replace /> },
  ],
);
