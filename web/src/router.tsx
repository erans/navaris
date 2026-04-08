import { createBrowserRouter, Navigate } from "react-router-dom";
import { lazy, type ReactNode } from "react";
import { Placeholder } from "@/routes/Placeholder";
import { RequireAuth } from "@/components/RequireAuth";

const Login = lazy(() => import("@/routes/Login"));

function protect(element: ReactNode) {
  return <RequireAuth>{element}</RequireAuth>;
}

export const router = createBrowserRouter(
  [
    { path: "/login", element: <Login /> },
    { path: "/", element: protect(<Placeholder label="home" />) },
    { path: "/projects", element: protect(<Placeholder label="projects" />) },
    { path: "/sandboxes", element: protect(<Placeholder label="sandboxes" />) },
    { path: "/sandboxes/:id", element: protect(<Placeholder label="sandbox detail" />) },
    { path: "/events", element: protect(<Placeholder label="events" />) },
    { path: "*", element: <Navigate to="/" replace /> },
  ],
);
