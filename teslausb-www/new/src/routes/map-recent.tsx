import { createFileRoute, redirect } from '@tanstack/react-router'

// Kept so old bookmarks land on the combined map.
export const Route = createFileRoute('/map-recent')({
  beforeLoad: () => {
    throw redirect({ to: '/map' })
  },
})
