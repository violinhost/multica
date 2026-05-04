// Velafi self-host fork: root `/` is NOT a marketing landing — every visitor
// goes to /login (which auto-redirects through OIDC). The upstream
// MulticaLanding / about / changelog / download pages are dead code in this
// deployment but kept under (landing)/ so the upstream merge surface stays
// small. Only this top-level page.tsx is overridden.
import { redirect } from "next/navigation";

export default function RootRedirect() {
  redirect("/login");
}
