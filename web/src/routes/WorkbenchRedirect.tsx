import { Navigate } from "react-router-dom";
import { useWorkbenchCompat } from "../resources/conversation-center/use-workbench-compat";

export function WorkbenchRedirect() {
  const redirectParams = useWorkbenchCompat();
  return <Navigate replace to={{ pathname: "/conversation-center", search: redirectParams.toString() }} />;
}
