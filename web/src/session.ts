// SPDX-License-Identifier: Apache-2.0

import { useQuery } from "@tanstack/react-query";
import { api, ApiError, unwrap, type Me } from "./api/client";

export function useSetup() {
  return useQuery({
    queryKey: ["setup"],
    queryFn: () => unwrap(api.GET("/setup/status")),
    staleTime: 5 * 60_000,
  });
}

/** The signed-in principal, or null when signed out. */
export function useMe() {
  return useQuery<Me | null>({
    queryKey: ["me"],
    queryFn: async () => {
      try {
        return await unwrap(api.GET("/auth/me"));
      } catch (e) {
        if (e instanceof ApiError && e.status === 401) return null;
        throw e;
      }
    },
    staleTime: 60_000,
  });
}
