/**
 * Which half of the first-run wizard is valid right now.
 *
 * The admin-only step posts to `/system/setup/admin`, an endpoint the **full**
 * application router mounts. While the bootstrap router serves the request
 * (database reachable, RAGFlow engine not usable yet) that endpoint cannot work,
 * so the operator must see the full form instead. Guessing from
 * `configured && db_up` was wrong for exactly that case: both are true in
 * bootstrap mode too, and the form ended in a 404 that looked like a dead button.
 *
 * `mode` is reported by the backend (`wizard` / `engine`). The fallback keeps
 * older payloads working.
 */
export interface SetupStatusLike {
	mode?: string;
	configured: boolean;
	db_up: boolean;
}

export const wantsAdminOnly = (status: SetupStatusLike): boolean =>
	status.mode ? status.mode === "engine" : status.configured && status.db_up;
