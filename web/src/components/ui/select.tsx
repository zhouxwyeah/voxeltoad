"use client"

import * as React from "react"
import { CheckIcon, ChevronsUpDown } from "lucide-react"

import { cn } from "@/lib/utils"
import {
  Command,
  CommandEmpty,
  CommandGroup,
  CommandInput,
  CommandItem,
  CommandList,
} from "@/components/ui/command"
import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from "@/components/ui/popover"

export type SelectOption = { value: string; label: string }

/**
 * Select primitive (design-system.md §3, §8).
 *
 * Single-select dropdown built on the existing Command + Popover primitives
 * (same building blocks as MultiSelect). Form-submittable via a hidden input
 * named after `name`, so it is a drop-in replacement for native <select> in
 * forms that read `formData.get(name)`.
 *
 * For filter bars with a small fixed option set, pass `searchable={false}`
 * (default) to omit the search input. Pass `searchable` for longer lists.
 */
export function Select({
  name,
  options,
  value,
  onValueChange,
  placeholder = "Select…",
  emptyText = "Nothing found.",
  searchable = false,
  className,
}: {
  /** FormData field name — submitted via a hidden input. */
  name: string
  options: SelectOption[]
  value: string
  onValueChange?: (v: string) => void
  placeholder?: string
  emptyText?: string
  searchable?: boolean
  className?: string
}) {
  const [open, setOpen] = React.useState(false)
  const [search, setSearch] = React.useState("")
  const selectedLabel = options.find((o) => o.value === value)?.label

  function handleSelect(selected: string) {
    onValueChange?.(selected)
    setOpen(false)
  }

  const filtered = searchable
    ? options.filter((o) =>
        o.label.toLowerCase().includes(search.toLowerCase()),
      )
    : options

  return (
    <>
      <input type="hidden" name={name} value={value} />

      <Popover open={open} onOpenChange={setOpen}>
        <PopoverTrigger
          role="combobox"
          aria-expanded={open}
          className={cn(
            "inline-flex h-8 items-center justify-between rounded border border-border bg-background px-2 text-xs text-foreground hover:bg-muted focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring disabled:pointer-events-none disabled:opacity-50",
            className,
          )}
        >
          <span className={cn("truncate", !selectedLabel && "text-muted-foreground")}>
            {selectedLabel ?? placeholder}
          </span>
          <ChevronsUpDown className="ml-2 h-3.5 w-3.5 shrink-0 opacity-50" />
        </PopoverTrigger>
        <PopoverContent
          className="w-[--radix-popover-trigger-width] p-0"
          align="start"
        >
          <Command
            filter={() => 1}
          >
            {searchable && (
              <CommandInput
                placeholder={`Search ${placeholder.toLowerCase()}…`}
                value={search}
                onValueChange={setSearch}
              />
            )}
            <CommandList>
              <CommandEmpty>{emptyText}</CommandEmpty>
              <CommandGroup>
                {filtered.map((option) => {
                  const isSelected = option.value === value
                  return (
                    <CommandItem
                      key={option.value}
                      value={option.value}
                      onSelect={() => handleSelect(option.value)}
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
                  )
                })}
              </CommandGroup>
            </CommandList>
          </Command>
        </PopoverContent>
      </Popover>
    </>
  )
}
