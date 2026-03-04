import { useEffect, useRef, useState } from "react"
import { cn } from "@/lib/utils"
import { Input } from "@/components/ui/input"

interface ComboboxOption {
  label: string
  value: string
}

interface ComboboxProps {
  options: ComboboxOption[]
  value: string
  onChange: (value: string) => void
  placeholder?: string
  className?: string
  loading?: boolean
  id?: string
  required?: boolean
}

export function Combobox({
  options,
  value,
  onChange,
  placeholder,
  className,
  loading,
  id,
  required,
}: ComboboxProps) {
  const [open, setOpen] = useState(false)
  const [highlightIndex, setHighlightIndex] = useState(-1)
  const containerRef = useRef<HTMLDivElement>(null)
  const listRef = useRef<HTMLUListElement>(null)

  const filtered = options.filter((o) =>
    o.label.toLowerCase().includes(value.toLowerCase())
  )

  useEffect(() => {
    setHighlightIndex(-1)
  }, [value])

  useEffect(() => {
    function handleClickOutside(e: MouseEvent) {
      if (containerRef.current && !containerRef.current.contains(e.target as Node)) {
        setOpen(false)
      }
    }
    document.addEventListener("mousedown", handleClickOutside)
    return () => document.removeEventListener("mousedown", handleClickOutside)
  }, [])

  useEffect(() => {
    if (highlightIndex >= 0 && listRef.current) {
      const item = listRef.current.children[highlightIndex] as HTMLElement | undefined
      item?.scrollIntoView({ block: "nearest" })
    }
  }, [highlightIndex])

  function handleKeyDown(e: React.KeyboardEvent) {
    if (!open) {
      if (e.key === "ArrowDown" || e.key === "ArrowUp") {
        setOpen(true)
        e.preventDefault()
      }
      return
    }

    switch (e.key) {
      case "ArrowDown":
        e.preventDefault()
        setHighlightIndex((i) => (i < filtered.length - 1 ? i + 1 : i))
        break
      case "ArrowUp":
        e.preventDefault()
        setHighlightIndex((i) => (i > 0 ? i - 1 : i))
        break
      case "Enter":
        if (highlightIndex >= 0 && highlightIndex < filtered.length) {
          e.preventDefault()
          onChange(filtered[highlightIndex].value)
          setOpen(false)
        }
        break
      case "Escape":
        setOpen(false)
        break
    }
  }

  return (
    <div ref={containerRef} className="relative">
      <Input
        id={id}
        value={value}
        onChange={(e) => {
          onChange(e.target.value)
          setOpen(true)
        }}
        onFocus={() => setOpen(true)}
        onKeyDown={handleKeyDown}
        placeholder={loading ? "Loading repositories..." : placeholder}
        className={cn("bg-zinc-950 border-zinc-700", className)}
        required={required}
        autoComplete="off"
        role="combobox"
        aria-expanded={open && filtered.length > 0}
        aria-autocomplete="list"
        aria-controls={id ? `${id}-listbox` : undefined}
      />
      {open && filtered.length > 0 && (
        <ul
          ref={listRef}
          id={id ? `${id}-listbox` : undefined}
          role="listbox"
          className="absolute z-50 mt-1 max-h-60 w-full overflow-auto rounded-md border border-zinc-700 bg-zinc-900 py-1 shadow-lg"
        >
          {filtered.map((option, i) => (
            <li
              key={option.value}
              role="option"
              aria-selected={highlightIndex === i}
              className={cn(
                "cursor-pointer px-3 py-2 text-sm text-zinc-50",
                highlightIndex === i && "bg-zinc-700",
                value === option.value && "font-medium"
              )}
              onMouseEnter={() => setHighlightIndex(i)}
              onMouseDown={(e) => {
                e.preventDefault()
                onChange(option.value)
                setOpen(false)
              }}
            >
              {option.label}
            </li>
          ))}
        </ul>
      )}
    </div>
  )
}
