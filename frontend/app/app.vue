<template>
  <div>
    <!-- Thin progress bar at top during route transitions -->
    <NuxtLoadingIndicator color="#3b82f6" :height="2" />
    <NuxtLayout>
      <NuxtPage />
    </NuxtLayout>
  </div>
</template>
