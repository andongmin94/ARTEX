import { create } from "zustand";

import type { AuthStatus } from "@/lib/types";

export const useAuthStore = create<{ mode: AuthStatus["mode"] | null }>(() => ({ mode: null }));
