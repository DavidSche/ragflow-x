import { render, screen } from "@testing-library/react";
import { MemoryRouter, Route, Routes, useLocation } from "react-router-dom";
import { describe, expect, it } from "vitest";
import { WorkbenchRedirect } from "./WorkbenchRedirect";

function LocationProbe() {
  const location = useLocation();
  return <span>{location.pathname + location.search}</span>;
}

describe("WorkbenchRedirect", () => {
  it.each([
    ["/workbench", "/conversation-center?kind=chat"],
    ["/workbench?chat=chat-1&sessionId=session-1", "/conversation-center?kind=chat&targetId=chat-1&contextId=session-1"],
    ["/workbench?chatId=chat-1", "/conversation-center?kind=chat&targetId=chat-1"],
    ["/workbench?sessionId=session-1&unknown=x", "/conversation-center?kind=chat"],
  ])("maps %s once without auto-submitting", (input, expected) => {
    render(
      <MemoryRouter initialEntries={[input]}>
        <Routes>
          <Route path="/workbench" element={<WorkbenchRedirect />} />
          <Route path="/conversation-center" element={<LocationProbe />} />
        </Routes>
      </MemoryRouter>,
    );

    expect(screen.getByText(expected)).toBeInTheDocument();
  });
});
