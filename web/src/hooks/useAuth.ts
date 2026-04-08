import { useQuery, useQueryClient } from "@tanstack/react-query";
import { getMe, login as loginAPI, logout as logoutAPI, type Me } from "@/api/session";

// authQueryKey is the single TanStack Query key for the current session. All
// login/logout/expiry invalidations target this key so every `useAuth` caller
// re-renders in sync.
export const authQueryKey = ["auth", "me"] as const;

export interface UseAuth {
  authenticated: boolean;
  expiresAt?: number;
  isLoading: boolean;
  login: (password: string) => Promise<void>;
  logout: () => Promise<void>;
}

export function useAuth(): UseAuth {
  const qc = useQueryClient();
  const q = useQuery<Me>({
    queryKey: authQueryKey,
    queryFn: getMe,
    // Session state doesn't benefit from aggressive refetching — the event
    // stream tells us when things change and failed requests tell us when
    // we're kicked out. Keep this cheap.
    staleTime: Infinity,
    retry: false,
  });

  return {
    authenticated: q.data?.authenticated === true,
    expiresAt: q.data?.expiresAt,
    isLoading: q.isLoading,
    async login(password: string) {
      // loginAPI now returns Me — we seed the cache directly instead of
      // going back to /ui/me again.
      const me = await loginAPI(password);
      qc.setQueryData<Me>(authQueryKey, me);
    },
    async logout() {
      await logoutAPI();
      qc.setQueryData<Me>(authQueryKey, { authenticated: false });
    },
  };
}
