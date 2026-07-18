import Link from "next/link";

/*
 * Local UI primitives (design/frontend.md §2, §11).
 *
 * Minimal styled wrappers over raw elements, driven by the design tokens in
 * globals.css. NOT shadcn/ui (no radix, no install) — just a thin local layer so
 * the palette lives in one place and the resource pages stay declarative. When
 * shadcn is introduced later (per the design doc), these map 1:1 onto it.
 *
 * Server-component safe: Button renders <button> by default, <a> when asChild
 * via `href`. No client hooks.
 */

type ButtonVariant = "primary" | "ghost" | "destructive" | "outline";

const buttonBase =
  "inline-flex items-center justify-center gap-1.5 rounded-md text-sm font-medium transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-1 focus-visible:ring-offset-background disabled:opacity-50 disabled:pointer-events-none whitespace-nowrap";

const buttonVariants: Record<ButtonVariant, string> = {
  primary: "bg-primary text-primary-foreground hover:bg-primary/90 shadow-sm",
  ghost: "text-muted-foreground hover:bg-accent hover:text-accent-foreground",
  destructive:
    "text-destructive hover:bg-destructive/10 hover:text-destructive",
  outline:
    "border border-border bg-background text-foreground hover:bg-accent hover:text-accent-foreground",
};

const buttonSizes = {
  sm: "h-8 px-3",
  md: "h-9 px-4",
};

/** Button. Use `href` to render a link (primary/ghost/outline/destructive). */
export function Button({
  variant = "primary",
  size = "md",
  className = "",
  href,
  children,
  ...props
}: {
  variant?: ButtonVariant;
  size?: keyof typeof buttonSizes;
  className?: string;
  href?: string;
} & React.ButtonHTMLAttributes<HTMLButtonElement> &
  React.AnchorHTMLAttributes<HTMLAnchorElement>) {
  const cls = `${buttonBase} ${buttonVariants[variant]} ${buttonSizes[size]} ${className}`;
  if (href) {
    return (
      <Link href={href} className={cls} {...props}>
        {children}
      </Link>
    );
  }
  return (
    <button className={cls} {...props}>
      {children}
    </button>
  );
}

/** Labeled text input. `label` renders above; error shows below in red. */
export function Input({
  label,
  name,
  error,
  className = "",
  ...props
}: {
  label?: string;
  name?: string;
  error?: string;
  className?: string;
} & React.InputHTMLAttributes<HTMLInputElement>) {
  return (
    <label className="flex flex-col gap-1 text-sm">
      {label && (
        <span className="font-medium text-foreground">{label}</span>
      )}
      <input
        name={name}
        className={`h-9 rounded-md border border-input bg-background px-3 text-sm text-foreground placeholder:text-muted-foreground/70 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-0 ${className}`}
        {...props}
      />
      {error && <span className="text-xs text-destructive">{error}</span>}
    </label>
  );
}

/** Card surface: white panel with hairline border + subtle rounding. */
export function Card({
  children,
  className = "",
}: {
  children: React.ReactNode;
  className?: string;
}) {
  return (
    <div
      className={`rounded-lg border border-border bg-background ${className}`}
    >
      {children}
    </div>
  );
}
