import type { AdminPaths as paths } from "@voxeltoad/gateway-sdk/admin";

export type BudgetPolicy = paths["/api/v1/budgets/{id}"]["get"]["responses"][200]["content"]["application/json"];
export type BudgetAccount = paths["/api/v1/budgets/{id}/accounts"]["get"]["responses"][200]["content"]["application/json"]["data"][number];
export type BudgetEvent = paths["/api/v1/budget-events"]["get"]["responses"][200]["content"]["application/json"]["data"][number];
export type Reservation = Pick<paths["/api/v1/billing-reservations"]["get"]["responses"][200]["content"]["application/json"]["data"][number], "id" | "request_id" | "status" | "estimate" | "actual" | "currency" | "reason" | "version" | "created_at">;
export type BudgetView = "policies" | "events" | "reservations";
