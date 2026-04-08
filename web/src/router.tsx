import { createBrowserRouter, Navigate } from "react-router-dom";
import { lazy } from "react";
import { Placeholder } from "@/routes/Placeholder";

const Login = lazy(() => import("@/routes/Login"));

export const router = createBrowserRouter(
  [
    { path: "/login", element: <Login /> },
    { path: "/", element: <Placeholder label="home" /> },
    { path: "/projects", element: <Placeholder label="projects" /> },
    { path: "/sandboxes", element: <Placeholder label="sandboxes" /> },
    { path: "/sandboxes/:id", element: <Placeholder label="sandbox detail" /> },
    { path: "/events", element: <Placeholder label="events" /> },
    { path: "*", element: <Navigate to="/" replace /> },
  ],
);
