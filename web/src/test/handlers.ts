import { http, HttpResponse } from "msw";

// Shared MSW handlers used across route and API tests. Individual tests
// override handlers as needed using server.use().
export const handlers = [
  http.get("/ui/me", () => HttpResponse.json({ authenticated: false }, { status: 401 })),
];
