import { createFileRoute } from '@tanstack/react-router'
import { TrackHistoryPage } from '../components/TrackHistoryPage'
import { validateMapSearch } from '../lib/timeRange'

export const Route = createFileRoute('/map')({
  validateSearch: validateMapSearch,
  component: TrackHistoryPage,
})
