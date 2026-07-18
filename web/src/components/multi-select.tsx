"use client";

import * as React from "react";
import { CheckIcon, ChevronsUpDown, X } from "lucide-react";

import { cn } from "@/lib/utils";
import {
  Command,
  CommandEmpty,
  CommandGroup,
  CommandInput,
  CommandItem,
  CommandList,
} from "@/components/ui/command";
import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from "@/components/ui/popover";
import { Badge } from "@/components/ui/badge";

type Option = { value: string; label: string };

export function MultiSelect({
  name,
  options,
  value,
  onChange,
  label,
  placeholder = "Select…",
  selectAllLabel = "Select all",
  emptyText = "Nothing found.",
}: {
  /** FormData field name — submits one hidden input per selected value. */
  name: string;
  options: Option[];
  value: string[];
  onChange: (v: string[]) => void;
  label?: string;
  placeholder?: string;
  selectAllLabel?: string;
  emptyText?: string;
}) {
  const [open, setOpen] = React.useState(false);

  const allSelected = options.length > 0 && value.length === options.length;
  const indeterminate = value.length > 0 && !allSelected;

  function toggle(optionValue: string) {
    const next = value.includes(optionValue)
      ? value.filter((v) => v !== optionValue)
      : [...value, optionValue];
    onChange(next);
  }

  function toggleAll() {
    onChange(allSelected ? [] : options.map((o) => o.value));
  }

  function remove(optionValue: string) {
    onChange(value.filter((v) => v !== optionValue));
  }

  return (
    <div className="flex flex-col gap-1.5 text-sm">
      {/* Hidden inputs for FormData submission (FormData.getAll compatible) */}
      {value.map((v) => (
        <input key={v} type="hidden" name={name} value={v} />
      ))}

      <span className="font-medium text-foreground">
        {label}
      </span>

      <Popover open={open} onOpenChange={setOpen}>
        <PopoverTrigger
          role="combobox"
          aria-expanded={open}
          className="inline-flex h-auto min-h-9 w-full items-center justify-between rounded-md border border-border bg-background px-3 py-2 text-sm font-normal text-foreground hover:bg-muted hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-0 disabled:pointer-events-none disabled:opacity-50"
        >
            <span className="truncate">
              {value.length === 0
                ? placeholder
                : value.length === options.length
                  ? selectAllLabel
                  : `${value.length} selected`}
            </span>
            <ChevronsUpDown className="ml-2 h-4 w-4 shrink-0 opacity-50" />
        </PopoverTrigger>
        <PopoverContent className="w-[--radix-popover-trigger-width] p-0" align="start">
          <Command
            filter={(val, search) => {
              const opt = options.find(
                (o) => o.value.toLowerCase() === val.toLowerCase(),
              );
              const label = opt?.label.toLowerCase() ?? val.toLowerCase();
              if (label.includes(search.toLowerCase())) return 1;
              return 0;
            }}
          >
            <CommandInput placeholder={`Search ${placeholder.toLowerCase()}…`} />
            <CommandList>
              <CommandEmpty>{emptyText}</CommandEmpty>
              <CommandGroup>
                {/* Select all */}
                <CommandItem
                  value="_select_all"
                  onSelect={toggleAll}
                  className="cursor-pointer"
                >
                  <div
                    className={cn(
                      "mr-2 flex h-4 w-4 shrink-0 items-center justify-center rounded-sm border border-primary",
                      allSelected
                        ? "bg-primary text-primary-foreground"
                        : indeterminate
                          ? "bg-primary/50 text-primary-foreground"
                          : "opacity-50 [&_svg]:invisible",
                    )}
                  >
                    <CheckIcon className="h-3 w-3" />
                  </div>
                  <span
                    className={
                      indeterminate ? "font-medium" : undefined
                    }
                  >
                    {selectAllLabel}
                  </span>
                </CommandItem>
              </CommandGroup>
              <CommandGroup>
                {options.map((option) => {
                  const isSelected = value.includes(option.value);
                  return (
                    <CommandItem
                      key={option.value}
                      value={option.value}
                      onSelect={() => toggle(option.value)}
                      className="cursor-pointer"
                    >
                      <div
                        className={cn(
                          "mr-2 flex h-4 w-4 shrink-0 items-center justify-center rounded-sm border border-primary",
                          isSelected
                            ? "bg-primary text-primary-foreground"
                            : "opacity-50 [&_svg]:invisible",
                        )}
                      >
                        <CheckIcon className="h-3 w-3" />
                      </div>
                      <span>{option.label}</span>
                    </CommandItem>
                  );
                })}
              </CommandGroup>
            </CommandList>
          </Command>
        </PopoverContent>
      </Popover>

      {/* Selected badges */}
      {value.length > 0 && (
        <div className="flex flex-wrap gap-1">
          {value.map((v) => {
            const label = options.find((o) => o.value === v)?.label ?? v;
            return (
              <Badge key={v} variant="secondary" className="gap-1 pr-1">
                {label}
                <button
                  type="button"
                  onClick={() => remove(v)}
                  className="ml-1 rounded-full outline-none ring-offset-background focus-visible:ring-2 focus-visible:ring-ring"
                >
                  <X className="h-3 w-3" />
                  <span className="sr-only">Remove {label}</span>
                </button>
              </Badge>
            );
          })}
        </div>
      )}
    </div>
  );
}
