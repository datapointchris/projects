<template>
  <AddEditModal
    :visible="visible"
    :focus-ref="reasonInput"
    @close="handleModalClose">
    <template #default="{ handleClose, handleSuccess }">
      <form
        class="add-edit-modal__form"
        @submit.prevent="handleSubmit(handleSuccess)">
        <h2>Drop {{ projectName }}</h2>

        <p class="drop-project-modal__note">
          Dropping closes the project without pretending it was finished. Its items are left exactly as they are.
        </p>

        <div class="add-edit-modal__form-item">
          <label for="project-drop-reason">Why is it dropped rather than deferred?</label>
          <textarea
            id="project-drop-reason"
            ref="reasonInput"
            v-model="reason"
            data-testid="project-drop-reason-input"
            rows="4"
            class="textbox drop-project-modal__reason-input"
            required></textarea>
        </div>

        <div class="add-edit-modal__form-buttons">
          <button
            type="submit"
            data-testid="project-drop-submit-button"
            class="button button--danger"
            :disabled="!reason.trim()">
            <span class="button__text button__text--danger">Drop Project</span>
          </button>
          <button
            type="button"
            data-testid="project-drop-cancel-button"
            class="button"
            @click="handleClose()">
            <span class="button__text">Cancel</span>
          </button>
        </div>
      </form>
    </template>
  </AddEditModal>
</template>

<script setup lang="ts">
import { ref } from 'vue'
import AddEditModal from '@/components/AddEditModal.vue'

defineProps<{
  visible: boolean
  projectName: string
}>()

const emit = defineEmits<{
  close: []
  drop: [reason: string]
}>()

const reasonInput = ref<HTMLTextAreaElement | null>(null)
const reason = ref('')

function handleModalClose() {
  reason.value = ''
  emit('close')
}

function handleSubmit(handleSuccess: () => void) {
  // The API refuses a blank reason too — a dropped project with no reason reads
  // as deferred, and deferred invites the same idea back next month.
  if (!reason.value.trim()) return
  emit('drop', reason.value.trim())
  handleSuccess()
}
</script>

<style scoped lang="scss">
.drop-project-modal__note {
  max-width: 400px;
  color: var(--clr-gray-500);
}

.drop-project-modal__reason-input {
  min-width: 400px;
  resize: vertical;
}
</style>
