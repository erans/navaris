import { clsx, type ClassValue } from "clsx";
import { twMerge } from "tailwind-merge";

// cn merges Tailwind classes with clsx + twMerge so conditional class names
// deduplicate conflicts. This is the standard shadcn/ui helper.
export function cn(...inputs: ClassValue[]) {
  return twMerge(clsx(inputs));
}
