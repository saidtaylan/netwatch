<template>
  <div class="flex h-screen overflow-hidden bg-gray-50 dark:bg-gray-900">
    <Sidebar />
    <div class="flex flex-col flex-1 overflow-hidden">
      <TopBar>
        <slot name="title" />
      </TopBar>
      <main class="flex-1 overflow-y-auto p-4 md:p-6">
        <slot />
      </main>
    </div>
    <Toast />
  </div>
</template>
