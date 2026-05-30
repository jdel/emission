import { useEffect, useRef, useState, type RefObject } from 'react'

// torrentFiles keeps only .torrent entries from a file list.
export function torrentFiles(list: Iterable<File> | null): File[] {
  if (!list) return []
  return Array.from(list).filter((f) => f.name.toLowerCase().endsWith('.torrent'))
}

// useFileDrop wires document-level drag-and-drop of .torrent files and returns
// whether a file drag is currently in progress (for the drop overlay).
export function useFileDrop(onFiles: (files: File[]) => void): boolean {
  const [isDragging, setIsDragging] = useState(false)
  const dragDepth = useRef(0)
  // Stable ref so handlers always call the latest onFiles.
  const onFilesRef = useRef(onFiles)
  useEffect(() => { onFilesRef.current = onFiles }, [onFiles])

  useEffect(() => {
    function onDragEnter(e: DragEvent) {
      if (!e.dataTransfer?.types.includes('Files')) return
      e.preventDefault()
      if (dragDepth.current++ === 0) setIsDragging(true)
    }
    function onDragLeave() {
      if (--dragDepth.current === 0) setIsDragging(false)
    }
    function onDragOver(e: DragEvent) {
      if (e.dataTransfer?.types.includes('Files')) e.preventDefault()
    }
    function onDrop(e: DragEvent) {
      e.preventDefault()
      dragDepth.current = 0
      setIsDragging(false)
      const files = torrentFiles(e.dataTransfer?.files ?? null)
      if (files.length) onFilesRef.current(files)
    }
    document.addEventListener('dragenter', onDragEnter)
    document.addEventListener('dragleave', onDragLeave)
    document.addEventListener('dragover', onDragOver)
    document.addEventListener('drop', onDrop)
    return () => {
      document.removeEventListener('dragenter', onDragEnter)
      document.removeEventListener('dragleave', onDragLeave)
      document.removeEventListener('dragover', onDragOver)
      document.removeEventListener('drop', onDrop)
    }
  }, [])

  return isDragging
}

// useUploadHotkey triggers onTrigger when the user presses "u" outside an input.
export function useUploadHotkey(onTrigger: () => void) {
  const onTriggerRef = useRef(onTrigger)
  useEffect(() => { onTriggerRef.current = onTrigger }, [onTrigger])

  useEffect(() => {
    function onKeyDown(e: KeyboardEvent) {
      if (e.key !== 'u' || e.ctrlKey || e.metaKey || e.altKey) return
      const el = document.activeElement
      if (el instanceof HTMLInputElement || el instanceof HTMLTextAreaElement) return
      onTriggerRef.current()
    }
    document.addEventListener('keydown', onKeyDown)
    return () => document.removeEventListener('keydown', onKeyDown)
  }, [])
}

// useInfiniteScroll calls onLoadMore when the sentinel scrolls into view.
export function useInfiniteScroll(
  sentinel: RefObject<HTMLElement | null>,
  enabled: boolean,
  onLoadMore: () => void,
) {
  const onLoadMoreRef = useRef(onLoadMore)
  useEffect(() => { onLoadMoreRef.current = onLoadMore }, [onLoadMore])

  useEffect(() => {
    const el = sentinel.current
    if (!el) return
    const obs = new IntersectionObserver((entries) => {
      if (entries[0].isIntersecting && enabled) onLoadMoreRef.current()
    })
    obs.observe(el)
    return () => obs.disconnect()
  }, [sentinel, enabled])
}
