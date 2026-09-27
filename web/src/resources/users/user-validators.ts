import { api } from "../../lib/api";

const EMAIL_PATTERN = /^[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}$/;

export function validateEmailFormat(value?: string) {
  const email = value?.trim();
  if (!email || EMAIL_PATTERN.test(email)) return undefined;
  return "email";
}

export async function validateUniqueUsername(value?: string, message = "username") {
  const username = value?.trim();
  if (!username) return undefined;
  const response = await api.get<{
    code: number;
    data: { items: Array<{ username: string }> };
  }>(`/users?scope=all&username=${encodeURIComponent(username)}&page=1&page_size=2`);
  const exists = (response.data?.data?.items ?? []).some(
    (user) => user.username.toLowerCase() === username.toLowerCase(),
  );
  return exists ? message : undefined;
}
