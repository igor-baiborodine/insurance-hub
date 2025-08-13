<template>
    <div>
        <PolicyList />
    </div>
</template>

<script>
    import PolicyList from "../components/PolicyList.vue";

    export default {
        name: 'policies',
        components: {
            PolicyList
        }
    }
</script>