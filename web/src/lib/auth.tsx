import {
  createContext,
  useCallback,
  useContext,
  useMemo,
  useState,
  type ReactNode,
} from "react";
import { api, unwrap } from "./api";
import { optionalWarning } from "./optional-error";

export interface CurrentUser {
  id: string;
  username: string;
  email?: string;
  tenant_id: string;
  role: string;
}

interface AuthState {
  user: CurrentUser | null;
  loading: boolean;
  login: (username: string, password: string) => Promise<void>;
  logout: () => void;
}

const AuthContext = createContext<AuthState | undefined>(undefined);

export function AuthProvider({ children }: { children: ReactNode }) {
  const [user, setUser] = useState<CurrentUser | null>(null);
  const [loading, setLoading] = useState(true);

  const login = useCallback(async (username: string, password: string) => {
    await unwrap<{ token: string }>(
      api.post("/auth/login", { username, password }),
    );
    await refreshUser();
  }, []);

  const refreshUser = useCallback(async () => {
    try {
      const data = await unwrap<CurrentUser>(api.get("/auth/me"));
      setUser(data);
    } catch {
      setUser(null);
    }
  }, []);

  const logout = useCallback(() => {
    void api.post("/auth/logout").catch((error) => optionalWarning(error, "退出登录请求失败"));
    setUser(null);
    window.location.href = "/login";
  }, []);

  // Load the current user once on boot if a token exists.
  useState(() => {
    refreshUser().finally(() => setLoading(false));
  });

  const value = useMemo<AuthState>(
    () => ({ user, loading, login, logout }),
    [user, loading, login, logout],
  );

  return <AuthContext.Provider value={value}>{children}</AuthContext.Provider>;
}

export function useAuth(): AuthState {
  const ctx = useContext(AuthContext);
  if (!ctx) throw new Error("useAuth must be used within AuthProvider");
  return ctx;
}
