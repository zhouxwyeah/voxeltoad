import { redirect } from "next/navigation";

// The Control Panel has no marketing home; send to the first dashboard page.
// The (dashboard) layout guard bounces to /login when unauthenticated.
export default function Home() {
  redirect("/providers");
}
