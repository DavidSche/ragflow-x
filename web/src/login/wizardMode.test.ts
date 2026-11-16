import {describe, expect, it} from "vitest";

import {wantsAdminOnly} from "./wizardMode";

describe("wantsAdminOnly", () => {
	it("offers the admin-only step only when the full router is serving", () => {
		expect(wantsAdminOnly({mode: "engine", configured: true, db_up: true})).toBe(true);
	});

	it("keeps the full form in bootstrap mode even with a reachable database", () => {
		// Regression: `start.sh --sqlite` reaches the wizard with the database up
		// (configured + db_up both true) but no engine yet, so only the bootstrap
		// router exists. Showing the admin-only form there posted to
		// /system/setup/admin and got a 404 — the trial run could not be finished.
		expect(wantsAdminOnly({mode: "wizard", configured: true, db_up: true})).toBe(false);
		expect(wantsAdminOnly({mode: "wizard", configured: false, db_up: false})).toBe(false);
	});

	it("falls back to the old heuristic when the backend omits mode", () => {
		expect(wantsAdminOnly({configured: true, db_up: true})).toBe(true);
		expect(wantsAdminOnly({configured: true, db_up: false})).toBe(false);
	});
});
