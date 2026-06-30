import * as React from "react";
import { cva, type VariantProps } from "class-variance-authority";
import { clsx } from "clsx";
import { Spinner } from "./spinner";

const buttonVariants = cva(
  "inline-flex items-center justify-center rounded-md text-sm font-medium transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-offset-2 disabled:pointer-events-none disabled:opacity-50",
  {
    variants: {
      variant: {
        default:
          "bg-blue-600 text-white hover:bg-blue-700 focus-visible:ring-blue-600",
        outline:
          "border border-gray-200 dark:border-gray-700 bg-white dark:bg-gray-900 hover:bg-gray-50 dark:hover:bg-gray-800 text-gray-900 dark:text-gray-100 focus-visible:ring-gray-300 dark:focus-visible:ring-gray-700",
        ghost:
          "hover:bg-gray-100 dark:hover:bg-gray-800 text-gray-900 dark:text-gray-100 focus-visible:ring-gray-300 dark:focus-visible:ring-gray-700",
        danger:
          "bg-red-600 text-white hover:bg-red-700 focus-visible:ring-red-600",
      },
      size: {
        sm: "h-8 rounded-md px-3 text-xs",
        md: "h-10 px-4 py-2",
        lg: "h-12 rounded-md px-8",
      },
    },
    defaultVariants: {
      variant: "default",
      size: "md",
    },
  }
);

export interface ButtonProps
  extends React.ButtonHTMLAttributes<HTMLButtonElement>,
    VariantProps<typeof buttonVariants> {
  isLoading?: boolean;
}

const Button = React.forwardRef<HTMLButtonElement, ButtonProps>(
  ({ className, variant, size, isLoading, children, disabled, ...props }, ref) => (
    <button
      ref={ref}
      className={clsx(buttonVariants({ variant, size }), className)}
      disabled={disabled || isLoading}
      {...props}
    >
      {isLoading && <Spinner size="sm" className="mr-2" />}
      {children}
    </button>
  )
);
Button.displayName = "Button";

export { Button, buttonVariants };
