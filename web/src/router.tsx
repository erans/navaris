import { createBrowserRouter } from "react-router-dom";
import { Placeholder } from "@/routes/Placeholder";

// Real routes are wired up in later tasks (Login in Task 22, Sandboxes
// in Task 27, etc). This is the initial shell so the app mounts cleanly.
export const router = createBrowserRouter([
  {
    path: "/",
    element: <Placeholder label="root" />,
  },
  {
    path: "/login",
    element: <Placeholder label="login" />,
  },
  {
    path: "/sandboxes",
    element: <Placeholder label="sandboxes" />,
  },
  {
    path: "*",
    element: <Placeholder label="not found" />,
  },
]);
