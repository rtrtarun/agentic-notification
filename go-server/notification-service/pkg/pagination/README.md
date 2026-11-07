# pagination

The cursor behind `?cursor=` on newest-first lists: the position of the last item received, `(created_at, id)`, encoded opaquely. The next page is everything strictly older, with the ID breaking ties, so pages do not shift when new items arrive. See [docs/milestones/milestone-32.md](../../../../docs/archived/milestones/milestone-32.md).
