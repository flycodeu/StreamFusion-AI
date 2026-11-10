import { request } from "./api";

export interface ListPage<T> { items: T[]; total: number }

// These screens need the complete set for filtering, installation status and dependency choices.
export async function requestAllPages<T extends { id: string }>(path: string, signal?: AbortSignal): Promise<T[]> {
  const separator = path.includes("?") ? "&" : "?";
  const first = await request<ListPage<T>>(`${path}${separator}page=1&pageSize=100`, "GET", undefined, signal);
  const items = [...first.items];
  const pages = Math.ceil(first.total / 100);
  for (let page = 2; page <= pages; page++) {
    const next = await request<ListPage<T>>(`${path}${separator}page=${page}&pageSize=100`, "GET", undefined, signal);
    items.push(...next.items);
  }
  return Array.from(new Map(items.map(item => [item.id, item])).values());
}
