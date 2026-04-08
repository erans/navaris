import { describe, it, expect, beforeAll, afterEach, afterAll } from "vitest";
import { render, screen } from "@testing-library/react";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { http, HttpResponse } from "msw";
import { setupServer } from "msw/node";
import Projects from "./Projects";

// Backend serialises domain.Project in PascalCase because the struct has no
// json tags; the listResponse envelope in internal/api/response.go is the
// only part that's lowercase. CreatedAt is an ISO-8601 string.
const server = setupServer(
  http.get("/v1/projects", () =>
    HttpResponse.json({
      data: [
        {
          ProjectID: "prj_1",
          Name: "default",
          CreatedAt: "2026-04-01T12:00:00Z",
          UpdatedAt: "2026-04-01T12:00:00Z",
          Metadata: null,
        },
        {
          ProjectID: "prj_2",
          Name: "staging",
          CreatedAt: "2026-04-02T09:30:00Z",
          UpdatedAt: "2026-04-02T09:30:00Z",
          Metadata: null,
        },
      ],
      pagination: null,
    }),
  ),
);
beforeAll(() => server.listen({ onUnhandledRequest: "error" }));
afterEach(() => server.resetHandlers());
afterAll(() => server.close());

function renderPage() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <MemoryRouter initialEntries={["/projects"]}>
        <Routes>
          <Route path="/projects" element={<Projects />} />
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

describe("Projects route", () => {
  it("renders project names", async () => {
    renderPage();
    expect(await screen.findByText("default")).toBeInTheDocument();
    expect(screen.getByText("staging")).toBeInTheDocument();
  });
});
