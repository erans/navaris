import type { ReactNode } from "react";
import { Navigate, useLocation } from "react-router-dom";
import { useAuth } from "@/hooks/useAuth";

// RequireAuth gates a subtree on a successful /ui/me check. While loading we
// render nothing (avoids a protected-page flash). On failure we redirect to
// /login and pass the current pathname as ?next= so the user returns where
// they started after signing in.
export function RequireAuth({ children }: { children: ReactNode }) {
  const { authenticated, isLoading } = useAuth();
  const location = useLocation();

  if (isLoading) return null;
  if (!authenticated) {
    const next = encodeURIComponent(location.pathname + location.search);
    return <Navigate to={`/login?next=${next}`} replace />;
  }

  return <>{children}</>;
}
