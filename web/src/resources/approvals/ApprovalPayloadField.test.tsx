import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { ApprovalPayloadField, tokenizeJSON } from "./ApprovalPayloadField";

vi.mock("@/components/ui/label", () => ({
  Label: ({ children, htmlFor }: any) => <label htmlFor={htmlFor}>{children}</label>,
}));

vi.mock("@/components/ui/textarea", () => ({
  Textarea: (props: any) => <textarea {...props} />,
}));

describe("ApprovalPayloadField", () => {
  it("tokenizes JSON values by syntax type", () => {
    expect(tokenizeJSON('{"name":"ragflow","count":2,"enabled":true}')).toEqual([
      { type: "keyword", text: "{" },
      { type: "key", text: '"name"' },
      { type: "keyword", text: ":" },
      { type: "string", text: '"ragflow"' },
      { type: "keyword", text: "," },
      { type: "key", text: '"count"' },
      { type: "keyword", text: ":" },
      { type: "number", text: "2" },
      { type: "keyword", text: "," },
      { type: "key", text: '"enabled"' },
      { type: "keyword", text: ":" },
      { type: "keyword", text: "true" },
      { type: "keyword", text: "}" },
    ]);
  });

  it("renders a formatted JSON preview", () => {
    render(
      <ApprovalPayloadField
        id="approval-payload"
        label="Request payload"
        value='{"name":"ragflow"}'
        invalidLabel="Invalid"
        onChange={() => {}}
      />,
    );

    const preview = screen.getByLabelText("Request payload preview");
    expect(preview.textContent).toBe('{\n  "name": "ragflow"\n}');
  });

  it("reports invalid JSON without a preview and supports editing", () => {
    const handleChange = vi.fn();
    render(
      <ApprovalPayloadField
        id="approval-payload"
        label="Request payload"
        value="{invalid}"
        invalidLabel="Request payload must be valid JSON"
        onChange={handleChange}
      />,
    );

    expect(screen.getByRole("alert")).toHaveTextContent(
      "Request payload must be valid JSON",
    );
    const input = screen.getByLabelText("Request payload");
    fireEvent.change(input, { target: { value: '{"valid":true}' } });
    expect(handleChange).toHaveBeenCalledWith('{"valid":true}');
  });
});
