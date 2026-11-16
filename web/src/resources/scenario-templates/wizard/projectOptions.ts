/**
 * Project choices for the wizard's step 3 picker.
 *
 * The API stores a project *id* and the wizard submits that id, but the field
 * used to be a free-text box pre-filled with the raw UUID — unreadable, and one
 * stray keystroke turned into a 400 from the server. Render names, submit ids.
 */
export interface ProjectLike {
  id: string;
  name?: string;
}

export interface ProjectChoice {
  id: string;
  name: string;
}

export function projectChoices(rows?: ProjectLike[] | null): ProjectChoice[] {
  return (rows ?? []).map((row) => ({
    id: row.id,
    // A project without a display name still has to be selectable and
    // distinguishable, so fall back to its id rather than rendering a blank row.
    name: row.name?.trim() || row.id,
  }));
}
