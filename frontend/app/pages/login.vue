<script setup lang="ts">
// Single-user self-hosted app — /login redirects to /setup
// This route kept so any old bookmarks still work.
await navigateTo({ name: 'setup' }, { replace: true })
</script>

<template><div /></template>
